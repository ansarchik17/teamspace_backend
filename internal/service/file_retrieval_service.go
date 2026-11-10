package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type fileCorpusReader interface {
	Lexical(context.Context, int64, []uuid.UUID, []string) ([]documentsearch.Passage, error)
	Semantic(context.Context, int64, []uuid.UUID, string, string, int) ([]documentsearch.Passage, error)
	Neighbors(context.Context, int64, []uuid.UUID, []documentsearch.Passage) ([]documentsearch.Passage, error)
	Coverage(context.Context, int64, []uuid.UUID) (int, int, error)
}
type fileIndexStore interface {
	Discover(context.Context) error
	Enqueue(context.Context, uuid.UUID) error
	Claim(context.Context, *uuid.UUID) (*repository.FileIndexJob, error)
	MarkReady(context.Context, repository.FileIndexJob, string, []documentsearch.TextChunk) error
	MarkFailed(context.Context, repository.FileIndexJob, string) error
	Delete(context.Context, []uuid.UUID) error
	PendingEmbeddingTexts(context.Context, string) ([]string, error)
}
type fileAccessReader interface {
	GetFileByID(context.Context, uuid.UUID) (*domain.DriveFile, error)
	ListAccessibleDriveFileIDs(context.Context, int64, []uuid.UUID) ([]uuid.UUID, error)
}

// FileRetrievalService owns Drive ingestion and retrieval; all AI entry points
// call it through the same user-scoped contract. No provider/billing bypasses.
type FileRetrievalService struct {
	corpus    fileCorpusReader
	index     fileIndexStore
	files     fileAccessReader
	storage   driveSearchFileDownloader
	extractor driveSearchExtractor
	semantic  *documentsearch.Search
	pipeline  *documentsearch.Pipeline
	logger    *zap.Logger
}

func NewFileRetrievalService(index *repository.FileSearchRepository, files *repository.DriveRepository, storage *FileService, extractor *ContentExtractionService, semantic *documentsearch.Search, pipeline *documentsearch.Pipeline, logger *zap.Logger) *FileRetrievalService {
	var downloader driveSearchFileDownloader
	if storage != nil {
		downloader = storage
	}
	return &FileRetrievalService{corpus: index, index: index, files: files, storage: downloader, extractor: extractor, semantic: semantic, pipeline: pipeline, logger: logger.With(zap.String("service", "file_retrieval"))}
}

func (s *FileRetrievalService) Enqueue(ctx context.Context, id uuid.UUID) error {
	return s.index.Enqueue(ctx, id)
}
func (s *FileRetrievalService) Delete(ctx context.Context, ids []uuid.UUID) error {
	return s.index.Delete(ctx, ids)
}

// WorkOnce runs in the existing document worker, not a second unmanaged queue.
func (s *FileRetrievalService) WorkOnce(ctx context.Context) error {
	if s.storage == nil || s.extractor == nil {
		return fmt.Errorf("file indexing dependencies are unavailable")
	}
	if err := s.index.Discover(ctx); err != nil {
		return err
	}
	job, err := s.index.Claim(ctx, nil)
	if err != nil {
		return err
	}
	if job != nil {
		if err = s.indexJob(ctx, *job); err != nil {
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			markErr := s.index.MarkFailed(failureCtx, *job, "extraction_failed")
			cancel()
			s.logger.Warn("file indexing failed", zap.String("file_id", job.FileID.String()), zap.Error(err))
			if markErr != nil {
				return markErr
			}
		}
	}
	if s.semantic != nil {
		texts, err := s.index.PendingEmbeddingTexts(ctx, s.semantic.ModelKey())
		if err != nil {
			return err
		}
		embeddingCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err = s.semantic.IndexTextsDeferred(embeddingCtx, texts)
		cancel()
		if err != nil {
			s.logger.Warn("file embedding backfill deferred", zap.Error(err))
		}
	}
	return nil
}
func (s *FileRetrievalService) indexJob(ctx context.Context, job repository.FileIndexJob) error {
	file, err := s.files.GetFileByID(ctx, job.FileID)
	if err != nil {
		return err
	}
	if file == nil || file.DeletedAt != nil || file.Scope != "drive" || !file.UpdatedAt.Equal(job.SourceUpdatedAt) {
		return fmt.Errorf("file source revision is unavailable")
	}
	if file.Size <= 0 || file.Size > AIDocumentMaxBytes {
		return fmt.Errorf("file exceeds extraction limit")
	}
	stream, _, size, err := s.storage.DownloadFile(ctx, file.ObjectName)
	if err != nil {
		return err
	}
	defer stream.Close()
	if size > AIDocumentMaxBytes {
		return fmt.Errorf("stored file exceeds extraction limit")
	}
	raw, err := io.ReadAll(io.LimitReader(stream, AIDocumentMaxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > AIDocumentMaxBytes {
		return fmt.Errorf("file exceeds bounded read limit")
	}
	text, err := extractFileText(ctx, s.extractor, file.RealName, raw)
	if err != nil {
		return err
	}
	text = notebookLineText(strings.TrimSpace(text))
	if text == "" {
		return fmt.Errorf("no readable text")
	}
	chunks := documentsearch.Split(text, 3000, 300)
	if err = s.index.MarkReady(ctx, job, text, chunks); err != nil {
		return err
	}
	s.logger.Info("file search index ready", zap.String("file_id", job.FileID.String()), zap.Int("chunks", len(chunks)))
	return nil
}

func (s *FileRetrievalService) Search(ctx context.Context, userID int64, req domain.DriveSearchQueryRequest) (*domain.DriveSearchQueryResponse, error) {
	started := time.Now()
	if userID <= 0 {
		return nil, apperrors.Unauthorized("authenticated user required")
	}
	query := strings.TrimSpace(req.Question)
	if query == "" || len([]rune(query)) > 2000 || len(req.FileIDs) > 32 {
		return nil, apperrors.BadRequest("invalid file search request")
	}
	ids := []uuid.UUID{}
	seenIDs := map[uuid.UUID]bool{}
	for _, raw := range req.FileIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, apperrors.BadRequest("invalid selected file ID")
		}
		if !seenIDs[id] {
			ids = append(ids, id)
			seenIDs[id] = true
		}
	}
	if len(ids) > 0 {
		allowed, err := s.files.ListAccessibleDriveFileIDs(ctx, userID, ids)
		if err != nil {
			return nil, err
		}
		set := map[uuid.UUID]bool{}
		for _, id := range allowed {
			set[id] = true
		}
		for _, id := range ids {
			if !set[id] {
				return nil, apperrors.Forbidden("selected file unavailable to this user")
			}
		}
		for _, id := range ids {
			if err := s.index.Enqueue(ctx, id); err != nil {
				return nil, err
			}
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}
	perFile := req.ChunksPerFile
	if perFile <= 0 {
		perFile = 5
	}
	if perFile > 8 {
		perFile = 8
	}
	plan := s.pipeline.Plan(ctx, query)
	lexical, err := s.corpus.Lexical(ctx, userID, ids, plan.Lexical)
	if err != nil {
		return nil, err
	}
	var semantic []documentsearch.Passage
	if s.semantic != nil {
		embeddingCtx, cancelEmbedding := context.WithTimeout(ctx, 5*time.Second)
		vectors, embedErr := s.semantic.Embed(embeddingCtx, []string{plan.Semantic})
		cancelEmbedding()
		if embedErr == nil {
			semantic, embedErr = s.corpus.Semantic(ctx, userID, ids, s.semantic.ModelKey(), vectors[0], s.semantic.Dimensions())
		}
		if embedErr != nil {
			s.logger.Warn("semantic file search unavailable; using lexical results", zap.Error(embedErr))
		}
	}
	ranked := s.pipeline.Rank(ctx, query, lexical, semantic, 60)
	// Authorize once more after model calls. The index never grants access and
	// a revoked result cannot reappear through a cached neighbor or metadata.
	neighbors, err := s.corpus.Neighbors(ctx, userID, ids, ranked)
	if err != nil {
		return nil, err
	}
	total, ready, err := s.corpus.Coverage(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	response := buildFileSearchResponse(query, ranked, neighbors, limit, perFile, 32000)
	response.Indexing = ready < total
	response.Incomplete = response.Indexing || (len(ids) > 0 && total < len(ids))
	if response.Indexing {
		response.Warnings = []string{"Поиск охватывает не все доступные файлы: некоторые ещё не обработаны или завершились с ошибкой."}
	}
	if len(ids) > 0 && total < len(ids) {
		response.Warnings = append(response.Warnings, "Не все выбранные файлы доступны для поиска по содержимому.")
	}
	s.logger.Info("file retrieval completed", zap.Int("lexical_candidates", len(lexical)), zap.Int("semantic_candidates", len(semantic)), zap.Int("sources", response.TotalFiles), zap.Int("ready_files", ready), zap.Int("searchable_files", total), zap.Bool("incomplete", response.Incomplete), zap.Duration("duration", time.Since(started)))
	return response, nil
}

// Bound evidence by files, chunks and characters. Match anchors precede their
// neighbors; context never gets promoted to an independent search match.
func buildFileSearchResponse(query string, ranked, neighbors []documentsearch.Passage, limit, perFile, budget int) *domain.DriveSearchQueryResponse {
	response := &domain.DriveSearchQueryResponse{Question: query, Sources: []domain.DriveSearchSource{}}
	current := map[string]documentsearch.Passage{}
	byDoc := map[string]map[int]documentsearch.Passage{}
	for _, p := range neighbors {
		current[p.ID] = p
		if byDoc[p.DocumentID] == nil {
			byDoc[p.DocumentID] = map[int]documentsearch.Passage{}
		}
		byDoc[p.DocumentID][p.Ordinal] = p
	}
	positions := map[string]int{}
	covered := map[string]bool{}
	for _, hit := range ranked {
		fresh, ok := current[hit.ID]
		if !ok || covered[hit.ID] {
			continue
		}
		fresh.Score = hit.Score
		position, exists := positions[hit.DocumentID]
		if !exists {
			if len(response.Sources) >= limit {
				continue
			}
			position = len(response.Sources)
			positions[hit.DocumentID] = position
			response.Sources = append(response.Sources, domain.DriveSearchSource{FileID: fresh.DocumentID, FileName: fresh.Name, Score: hit.Score, Chunks: []domain.DriveSearchChunk{}})
		}
		source := &response.Sources[position]
		if len(source.ChunkIndices) >= perFile || len([]rune(fresh.Text)) > budget {
			continue
		}
		source.ChunkIndices = append(source.ChunkIndices, fresh.Ordinal)
		source.Chunks = append(source.Chunks, domain.DriveSearchChunk{ChunkIndex: fresh.Ordinal, Content: fresh.Text, Score: hit.Score, Locator: fresh.Locator})
		covered[fresh.ID] = true
		budget -= len([]rune(fresh.Text))
		for _, ordinal := range []int{fresh.Ordinal - 1, fresh.Ordinal + 1} {
			p, ok := byDoc[fresh.DocumentID][ordinal]
			if !ok || covered[p.ID] || len([]rune(p.Text)) > budget {
				continue
			}
			source.Chunks = append(source.Chunks, domain.DriveSearchChunk{ChunkIndex: p.Ordinal, Content: p.Text, IsContext: true, Locator: p.Locator})
			covered[p.ID] = true
			budget -= len([]rune(p.Text))
		}
	}
	filtered := response.Sources[:0]
	for _, source := range response.Sources {
		if len(source.ChunkIndices) > 0 {
			filtered = append(filtered, source)
		}
	}
	response.Sources = filtered
	response.TotalFiles = len(filtered)
	return response
}

// Production extractors propagate request/job cancellation into OCR processes
// and their shared parser semaphore. Legacy adapters remain compatible.
func extractFileText(ctx context.Context, extractor driveSearchExtractor, name string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if contextual, ok := extractor.(interface {
		ExtractTextBytesContext(context.Context, string, []byte) (string, error)
	}); ok {
		return contextual.ExtractTextBytesContext(ctx, name, data)
	}
	return extractor.ExtractTextBytes(name, data)
}

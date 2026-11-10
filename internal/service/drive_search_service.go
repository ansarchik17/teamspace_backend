package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const maxOnDemandDriveSearchIndexFiles = 10

type driveSearchRepository interface {
	GetActiveDriveFilesByIDs(ctx context.Context, fileIDs []uuid.UUID) ([]*domain.DriveFile, error)
	ListAccessibleDriveFileIDs(ctx context.Context, userID int64, fileIDs []uuid.UUID) ([]uuid.UUID, error)
	GetFileByID(ctx context.Context, fileID uuid.UUID) (*domain.DriveFile, error)
	ListUsersWithAccess(ctx context.Context, resourceType domain.ResourceType, resourceID uuid.UUID) ([]domain.UserPermission, error)
}

type driveSearchFileDownloader interface {
	DownloadFile(ctx context.Context, objectName string) (io.ReadCloser, string, int64, error)
}

type driveSearchExtractor interface {
	ExtractTextBytes(filename string, buf []byte) (string, error)
}

type driveSearchEmbedder interface {
	CreateChatCompletion(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error)
	CreateEmbeddings(ctx context.Context, req alem.EmbeddingRequest) (*alem.EmbeddingResponse, error)
	Rerank(ctx context.Context, req alem.RerankRequest) (*alem.RerankResponse, error)
}

type driveSearchQueryContext struct {
	OriginalQuestion string
	RewrittenQuery   string
	HypotheticalDoc  string
}

type localFileRetrieval interface {
	Search(context.Context, int64, domain.DriveSearchQueryRequest) (*domain.DriveSearchQueryResponse, error)
	Enqueue(context.Context, uuid.UUID) error
	Delete(context.Context, []uuid.UUID) error
}

type DriveSearchService struct {
	local     localFileRetrieval
	cfg       config.DriveSearchConfig
	client    *http.Client
	logger    *zap.Logger
	repo      driveSearchRepository
	fileSvc   driveSearchFileDownloader
	extractor driveSearchExtractor
	embedder  driveSearchEmbedder
}

type DriveSearchOption func(*DriveSearchService)

func WithDriveIndexDependencies(repo *repository.DriveRepository, fileSvc *FileService, extractor *ContentExtractionService) DriveSearchOption {
	return func(s *DriveSearchService) {
		// A nil pointer stored in an interface is not == nil; storage may be
		// unavailable at startup (fileSvc is nil when MinIO is down).
		if repo != nil {
			s.repo = repo
		}
		if fileSvc != nil {
			s.fileSvc = fileSvc
		}
		if extractor != nil {
			s.extractor = extractor
		}
	}
}

func WithDriveSearchEmbedder(embedder driveSearchEmbedder) DriveSearchOption {
	return func(s *DriveSearchService) {
		s.embedder = embedder
	}
}

func NewDriveSearchService(cfg config.DriveSearchConfig, logger *zap.Logger, opts ...DriveSearchOption) *DriveSearchService {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	svc := &DriveSearchService{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
		logger: logger.With(zap.String("service", "drive_search")),
	}
	if svc.hybridEnabled() && strings.TrimSpace(cfg.AlemAPIKey) != "" {
		svc.embedder = unaccountedDriveProvider{}
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// SetLocalRetrieval selects PostgreSQL ingestion/retrieval without changing the
// public Drive search contract. Elasticsearch remains an explicit legacy adapter.
func (s *DriveSearchService) SetLocalRetrieval(local localFileRetrieval) { s.local = local }

func (s *DriveSearchService) Enabled() bool {
	return s.local != nil || (strings.TrimSpace(s.cfg.ElasticsearchURL) != "" && strings.TrimSpace(s.cfg.Index) != "")
}

func (s *DriveSearchService) Search(ctx context.Context, userID int64, req domain.DriveSearchQueryRequest) (*domain.DriveSearchQueryResponse, error) {
	if s.local != nil {
		return s.local.Search(ctx, userID, req)
	}
	if userID <= 0 {
		return nil, fmt.Errorf("user_id is required")
	}
	if !s.Enabled() {
		return nil, fmt.Errorf("drive search is not configured")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = s.cfg.ResultLimit
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	chunksPerFile := req.ChunksPerFile
	if chunksPerFile <= 0 {
		chunksPerFile = s.cfg.ChunksPerFile
	}
	if chunksPerFile <= 0 {
		chunksPerFile = 5
	}
	if chunksPerFile > 8 {
		chunksPerFile = 8
	}

	if s.repo == nil {
		return nil, fmt.Errorf("drive search access repository is required")
	}
	if len(req.FileIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(req.FileIDs))
		for _, raw := range req.FileIDs {
			id, err := uuid.Parse(strings.TrimSpace(raw))
			if err != nil {
				continue
			}
			ids = append(ids, id)
		}
		allowed, err := s.repo.ListAccessibleDriveFileIDs(ctx, userID, ids)
		if err != nil {
			return nil, fmt.Errorf("check requested drive files: %w", err)
		}
		requested := make(map[uuid.UUID]struct{}, len(ids))
		for _, id := range ids {
			requested[id] = struct{}{}
		}
		req.FileIDs = nil
		for _, id := range allowed {
			if _, ok := requested[id]; ok {
				req.FileIDs = append(req.FileIDs, id.String())
			}
		}
		if len(req.FileIDs) == 0 {
			return &domain.DriveSearchQueryResponse{Question: req.Question}, nil
		}
	}

	if len(req.FileIDs) > 0 {
		if _, err := s.indexMissingFiles(ctx, req.FileIDs); err != nil {
			s.logger.Warn("drive search on-demand indexing failed for missing files",
				zap.Error(err),
				zap.Strings("file_ids", req.FileIDs),
			)
		}
	}

	return s.searchOnce(ctx, userID, req, limit, chunksPerFile)
}

func (s *DriveSearchService) searchOnce(ctx context.Context, userID int64, req domain.DriveSearchQueryRequest, limit, chunksPerFile int) (*domain.DriveSearchQueryResponse, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("drive search access repository is required")
	}
	queryCtx, err := s.prepareSearchQueryContext(ctx, req.Question)
	if err != nil {
		s.logger.Warn("drive search query rewrite failed, using original question", zap.Error(err))
		queryCtx = &driveSearchQueryContext{
			OriginalQuestion: req.Question,
			RewrittenQuery:   strings.TrimSpace(req.Question),
			HypotheticalDoc:  "",
		}
	}
	if queryCtx == nil {
		queryCtx = &driveSearchQueryContext{
			OriginalQuestion: req.Question,
			RewrittenQuery:   strings.TrimSpace(req.Question),
			HypotheticalDoc:  "",
		}
	}

	filterClauses := s.buildSearchFilterClauses(userID, req.FileIDs)

	bm25Resp, err := s.searchElastic(ctx, s.buildLexicalSearchPayload(limit, chunksPerFile, filterClauses, s.buildSearchShouldClauses(queryCtx.RewrittenQuery)))
	if err != nil {
		return nil, err
	}

	combinedHits := bm25Resp.Hits.Hits
	if vectorResp, vectorErr := s.searchSemantic(ctx, queryCtx, userID, req.FileIDs, limit, chunksPerFile, filterClauses); vectorErr != nil {
		s.logger.Warn("drive semantic search failed, keeping lexical results only", zap.Error(vectorErr))
	} else if vectorResp != nil {
		combinedHits = s.mergeSearchHits(bm25Resp.Hits.Hits, vectorResp.Hits.Hits)
	}

	if len(combinedHits) == 0 {
		return &domain.DriveSearchQueryResponse{Question: req.Question, TotalFiles: 0, Sources: nil}, nil
	}

	fileMetaByID, accessibleByID, err := s.loadVisibleSearchFiles(ctx, userID, combinedHits)
	if err != nil {
		return nil, err
	}

	// The index ACL can be stale. Remove inaccessible hits before sending
	// names or document text to the external reranker.
	visible := make([]elasticDriveFileHit, 0, len(combinedHits))
	for _, hit := range combinedHits {
		id, err := uuid.Parse(strings.TrimSpace(hit.Source.FileID))
		if err != nil {
			continue
		}
		if _, ok := accessibleByID[id]; !ok {
			continue
		}
		visible = append(visible, hit)
	}
	combinedHits = visible
	if rerankedHits, rerankErr := s.rerankSearchHits(ctx, queryCtx, combinedHits); rerankErr != nil {
		s.logger.Warn("drive rerank failed, keeping hybrid results", zap.Error(rerankErr))
	} else if rerankedHits != nil {
		combinedHits = rerankedHits
	}

	sources := make([]domain.DriveSearchSource, 0, len(combinedHits))
	for _, hit := range combinedHits {
		source := domain.DriveSearchSource{
			FileID:     hit.Source.FileID,
			FileName:   hit.Source.FileName,
			FolderPath: hit.Source.FolderPath,
			UpdatedAt:  hit.Source.UpdatedAt,
			Score:      hit.Score,
		}
		fileID, err := uuid.Parse(strings.TrimSpace(hit.Source.FileID))
		if err != nil {
			continue
		}
		if _, ok := accessibleByID[fileID]; !ok {
			continue
		}
		if fileMeta, ok := fileMetaByID[fileID]; ok {
			source.FileName = fileMeta.RealName
			source.UpdatedAt = fileMeta.UpdatedAt.Format(time.RFC3339Nano)
		}
		for _, innerHit := range hit.InnerHits.TopChunksPerFile.Hits.Hits {
			source.ChunkIndices = append(source.ChunkIndices, innerHit.Source.ChunkIndex)
			source.Chunks = append(source.Chunks, domain.DriveSearchChunk{
				ChunkIndex:    innerHit.Source.ChunkIndex,
				ChunkCount:    innerHit.Source.ChunkCount,
				SectionIndex:  innerHit.Source.SectionIndex,
				SectionID:     innerHit.Source.SectionID,
				ParentContent: innerHit.Source.SectionContent,
				Content:       innerHit.Source.Content,
				Highlights:    innerHit.Highlight.Content,
				Score:         innerHit.Score,
			})
		}
		sources = append(sources, source)
	}

	if err := s.expandSourceChunkContext(ctx, userID, sources); err != nil {
		s.logger.Warn("drive chunk context expansion failed, returning child chunks only", zap.Error(err))
	}

	return &domain.DriveSearchQueryResponse{
		Question:   req.Question,
		TotalFiles: len(sources),
		Sources:    sources,
	}, nil
}

func (s *DriveSearchService) expandSourceChunkContext(ctx context.Context, userID int64, sources []domain.DriveSearchSource) error {
	window := s.cfg.ParentContextWindow
	if window <= 0 || len(sources) == 0 {
		return nil
	}

	for i := range sources {
		if len(sources[i].Chunks) == 0 {
			continue
		}
		if hasSectionParentContent(sources[i].Chunks) {
			continue
		}

		parentContentBySectionID, err := s.fetchSectionParentContent(ctx, userID, sources[i].FileID, sources[i].Chunks)
		if err != nil {
			return err
		}
		if len(parentContentBySectionID) > 0 {
			sources[i] = applySectionParentContent(sources[i], parentContentBySectionID)
		}
		if hasSectionParentContent(sources[i].Chunks) {
			continue
		}

		indices := make(map[int]struct{})
		chunkCount := 0
		for _, chunk := range sources[i].Chunks {
			if chunk.ChunkCount > chunkCount {
				chunkCount = chunk.ChunkCount
			}
			for offset := -window; offset <= window; offset++ {
				idx := chunk.ChunkIndex + offset
				if idx < 0 {
					continue
				}
				if chunkCount > 0 && idx >= chunkCount {
					continue
				}
				indices[idx] = struct{}{}
			}
		}
		if len(indices) == 0 {
			continue
		}

		contextChunks, err := s.fetchChunkContext(ctx, userID, sources[i].FileID, indices)
		if err != nil {
			return err
		}
		sources[i] = mergeDriveSearchChunks(sources[i], contextChunks)
	}

	return nil
}

func hasSectionParentContent(chunks []domain.DriveSearchChunk) bool {
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.ParentContent) == "" {
			return false
		}
	}
	return len(chunks) > 0
}

func applySectionParentContent(source domain.DriveSearchSource, parentContentBySectionID map[string]string) domain.DriveSearchSource {
	if len(parentContentBySectionID) == 0 {
		return source
	}
	for i := range source.Chunks {
		if parentContent, ok := parentContentBySectionID[strings.TrimSpace(source.Chunks[i].SectionID)]; ok && strings.TrimSpace(parentContent) != "" {
			source.Chunks[i].ParentContent = parentContent
		}
	}
	return source
}

func (s *DriveSearchService) fetchSectionParentContent(ctx context.Context, userID int64, fileID string, chunks []domain.DriveSearchChunk) (map[string]string, error) {
	sectionIDs := make([]string, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		sectionID := strings.TrimSpace(chunk.SectionID)
		if sectionID == "" {
			continue
		}
		if _, ok := seen[sectionID]; ok {
			continue
		}
		seen[sectionID] = struct{}{}
		sectionIDs = append(sectionIDs, sectionID)
	}
	if len(sectionIDs) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(map[string]any{
		"size": len(sectionIDs),
		"_source": []string{
			"section_id",
			"section_content",
		},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"file_id": fileID}},
					map[string]any{
						"term": map[string]any{
							"allowed_user_ids": userID,
						},
					},
					map[string]any{
						"term": map[string]any{
							"doc_kind": "section_parent",
						},
					},
					map[string]any{
						"terms": map[string]any{
							"section_id": sectionIDs,
						},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal section parent query: %w", err)
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_search"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("section parent search failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}

	var resp struct {
		Hits struct {
			Hits []struct {
				Source struct {
					SectionID      string `json:"section_id"`
					SectionContent string `json:"section_content"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode section parent response: %w", err)
	}

	result := make(map[string]string, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		sectionID := strings.TrimSpace(hit.Source.SectionID)
		if _, ok := seen[sectionID]; !ok {
			continue
		}
		if strings.TrimSpace(hit.Source.SectionContent) == "" {
			continue
		}
		result[sectionID] = hit.Source.SectionContent
	}
	return result, nil
}

func (s *DriveSearchService) fetchChunkContext(ctx context.Context, userID int64, fileID string, chunkIndices map[int]struct{}) ([]elasticDriveChunkContextHit, error) {
	if len(chunkIndices) == 0 {
		return nil, nil
	}

	indices := make([]int, 0, len(chunkIndices))
	for idx := range chunkIndices {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	body, err := json.Marshal(map[string]any{
		"size": len(indices),
		"_source": []string{
			"chunk_index",
			"chunk_count",
			"section_index",
			"section_id",
			"section_content",
			"content",
		},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{
						"term": map[string]any{
							"allowed_user_ids": userID,
						},
					},
					map[string]any{
						"term": map[string]any{
							"doc_kind": "section_chunk",
						},
					},
					map[string]any{
						"term": map[string]any{
							"file_id": fileID,
						},
					},
					map[string]any{
						"terms": map[string]any{
							"chunk_index": indices,
						},
					},
				},
			},
		},
		"sort": []any{
			map[string]any{"chunk_index": "asc"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal drive chunk context query: %w", err)
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_search"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("drive chunk context search failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}

	var resp elasticDriveChunkContextResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode drive chunk context response: %w", err)
	}
	return resp.Hits.Hits, nil
}

func (s *DriveSearchService) buildSearchFilterClauses(userID int64, fileIDs []string) []any {
	filterClauses := []any{
		map[string]any{
			"term": map[string]any{"allowed_user_ids": userID},
		},
	}
	if len(fileIDs) > 0 {
		filterClauses = append(filterClauses, map[string]any{
			"terms": map[string]any{"file_id": fileIDs},
		})
	}
	filterClauses = append(filterClauses, map[string]any{
		"term": map[string]any{"doc_kind": "section_chunk"},
	})
	return filterClauses
}

func mergeDriveSearchChunks(source domain.DriveSearchSource, contextChunks []elasticDriveChunkContextHit) domain.DriveSearchSource {
	if len(contextChunks) == 0 {
		return source
	}

	type chunkAgg struct {
		chunk domain.DriveSearchChunk
	}

	merged := make(map[int]*chunkAgg, len(source.Chunks)+len(contextChunks))
	for _, chunk := range source.Chunks {
		chunk.IsContext = false
		merged[chunk.ChunkIndex] = &chunkAgg{chunk: chunk}
	}

	for _, hit := range contextChunks {
		if _, exists := merged[hit.Source.ChunkIndex]; exists {
			continue
		}
		merged[hit.Source.ChunkIndex] = &chunkAgg{
			chunk: domain.DriveSearchChunk{
				ChunkIndex:    hit.Source.ChunkIndex,
				ChunkCount:    hit.Source.ChunkCount,
				SectionIndex:  hit.Source.SectionIndex,
				SectionID:     hit.Source.SectionID,
				ParentContent: hit.Source.SectionContent,
				Content:       hit.Source.Content,
				Score:         hit.Score,
				IsContext:     true,
			},
		}
	}

	indices := make([]int, 0, len(merged))
	for idx := range merged {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	source.Chunks = source.Chunks[:0]
	source.ChunkIndices = source.ChunkIndices[:0]
	for _, idx := range indices {
		chunk := merged[idx].chunk
		source.Chunks = append(source.Chunks, chunk)
		source.ChunkIndices = append(source.ChunkIndices, idx)
	}
	return source
}

func (s *DriveSearchService) buildLexicalSearchPayload(limit, chunksPerFile int, filterClauses, shouldClauses []any) map[string]any {
	return map[string]any{
		"size": limit,
		"_source": []string{
			"file_id",
			"file_name",
			"folder_path",
			"updated_at",
		},
		"query": map[string]any{
			"bool": map[string]any{
				"filter":               filterClauses,
				"should":               shouldClauses,
				"minimum_should_match": 1,
			},
		},
		"collapse": map[string]any{
			"field": "file_id",
			"inner_hits": map[string]any{
				"name": "top_chunks_per_file",
				"size": chunksPerFile,
				"sort": []any{"_score"},
				"_source": []string{
					"chunk_index",
					"chunk_count",
					"section_index",
					"section_id",
					"section_content",
					"content",
				},
				"highlight": map[string]any{
					"fields": map[string]any{
						"content": map[string]any{
							"fragment_size":       220,
							"number_of_fragments": 2,
						},
					},
				},
			},
		},
		"sort": []any{
			"_score",
			map[string]any{"updated_at": "desc"},
		},
	}
}

func (s *DriveSearchService) buildSemanticSearchPayload(ctx context.Context, queryCtx *driveSearchQueryContext, userID int64, fileIDs []string, limit, chunksPerFile int, filterClauses []any) (map[string]any, error) {
	if !s.hybridEnabled() || s.embedder == nil {
		return nil, nil
	}

	semanticText := strings.TrimSpace(queryCtx.HypotheticalDoc)
	if semanticText == "" {
		semanticText = strings.TrimSpace(queryCtx.RewrittenQuery)
	}
	if semanticText == "" {
		semanticText = strings.TrimSpace(queryCtx.OriginalQuestion)
	}

	vectors, err := s.embedTexts(ctx, []string{semanticText})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("embedding response is empty")
	}

	k := s.cfg.HybridK
	if k <= 0 {
		k = limit * chunksPerFile
	}
	if k < limit {
		k = limit
	}

	numCandidates := s.cfg.NumCandidates
	if numCandidates <= 0 {
		numCandidates = k * 2
	}
	if numCandidates < k {
		numCandidates = k
	}

	return map[string]any{
		"size": limit,
		"_source": []string{
			"file_id",
			"file_name",
			"folder_path",
			"updated_at",
		},
		"knn": map[string]any{
			"field":          s.vectorField(),
			"query_vector":   vectors[0],
			"k":              k,
			"num_candidates": numCandidates,
			"filter":         filterClauses,
		},
		"collapse": map[string]any{
			"field": "file_id",
			"inner_hits": map[string]any{
				"name": "top_chunks_per_file",
				"size": chunksPerFile,
				"sort": []any{"_score"},
				"_source": []string{
					"chunk_index",
					"chunk_count",
					"content",
				},
				"highlight": map[string]any{
					"fields": map[string]any{
						"content": map[string]any{
							"fragment_size":       220,
							"number_of_fragments": 2,
						},
					},
				},
			},
		},
		"sort": []any{
			"_score",
			map[string]any{"updated_at": "desc"},
		},
	}, nil
}

func (s *DriveSearchService) searchSemantic(ctx context.Context, queryCtx *driveSearchQueryContext, userID int64, fileIDs []string, limit, chunksPerFile int, filterClauses []any) (*elasticDriveSearchResponse, error) {
	payload, err := s.buildSemanticSearchPayload(ctx, queryCtx, userID, fileIDs, limit, chunksPerFile, filterClauses)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	return s.searchElastic(ctx, payload)
}

func (s *DriveSearchService) prepareSearchQueryContext(ctx context.Context, question string) (*driveSearchQueryContext, error) {
	normalized := strings.TrimSpace(question)
	if normalized == "" {
		return &driveSearchQueryContext{OriginalQuestion: question, RewrittenQuery: question}, nil
	}
	if !s.queryRewriteEnabled() || s.embedder == nil {
		return &driveSearchQueryContext{
			OriginalQuestion: question,
			RewrittenQuery:   normalized,
		}, nil
	}

	rewritten, err := s.rewriteSearchQuery(ctx, normalized)
	if err != nil {
		return nil, err
	}

	hypotheticalDoc, hydeErr := s.generateHypotheticalDoc(ctx, rewritten)
	if hydeErr != nil {
		s.logger.Warn("drive search hyde generation failed, continuing without hypothetical doc", zap.Error(hydeErr))
	}

	return &driveSearchQueryContext{
		OriginalQuestion: question,
		RewrittenQuery:   rewritten,
		HypotheticalDoc:  hypotheticalDoc,
	}, nil
}

func (s *DriveSearchService) rewriteSearchQuery(ctx context.Context, question string) (string, error) {
	req := alem.ChatCompletionRequest{
		Model: s.queryRewriteModel(),
		Messages: []alem.Message{
			{
				Role:    "system",
				Content: "You transform user search queries for document retrieval. Return strict JSON only with the field rewritten_query. rewritten_query must be a concise search query that preserves names, numbers, and acronyms while expanding shorthand. Do not add markdown, code fences, or commentary.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("User query: %s", question),
			},
		},
		Temperature: 0.1,
		MaxTokens:   s.queryRewriteMaxTokens(),
	}

	resp, err := s.embedder.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return question, nil
	}

	content := extractChatContentText(resp.Choices[0].Message.Content)
	if content == "" {
		return question, nil
	}

	parsed := struct {
		RewrittenQuery string `json:"rewritten_query"`
	}{}
	if err := decodeSearchRewriteJSON(content, &parsed); err != nil {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return question, nil
		}
		return trimmed, nil
	}

	rewritten := strings.TrimSpace(parsed.RewrittenQuery)
	if rewritten == "" {
		return question, nil
	}
	return rewritten, nil
}

func (s *DriveSearchService) generateHypotheticalDoc(ctx context.Context, rewrittenQuery string) (string, error) {
	req := alem.ChatCompletionRequest{
		Model: s.hydeModel(),
		Messages: []alem.Message{
			{
				Role:    "system",
				Content: "You write a short hypothetical document passage for HyDE retrieval. Return plain text only, 3 to 5 sentences, no markdown, no bullets, no caveats. The passage should sound like a plausible document answering the query.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Query: %s", rewrittenQuery),
			},
		},
		Temperature: 0.2,
		MaxTokens:   s.hydeMaxTokens(),
	}

	resp, err := s.embedder.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", nil
	}

	content := strings.TrimSpace(extractChatContentText(resp.Choices[0].Message.Content))
	return content, nil
}

func (s *DriveSearchService) searchElastic(ctx context.Context, queryPayload map[string]any) (*elasticDriveSearchResponse, error) {
	body, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal elastic query: %w", err)
	}

	url := strings.TrimRight(s.cfg.ElasticsearchURL, "/") + "/" + strings.Trim(s.cfg.Index, "/") + "/_search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create elastic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("perform elastic request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read elastic response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("elastic search failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var esResp elasticDriveSearchResponse
	if err := json.Unmarshal(respBody, &esResp); err != nil {
		return nil, fmt.Errorf("decode elastic response: %w", err)
	}
	return &esResp, nil
}

func (s *DriveSearchService) mergeSearchHits(bm25Hits, semanticHits []elasticDriveFileHit) []elasticDriveFileHit {
	if len(bm25Hits) == 0 && len(semanticHits) == 0 {
		return nil
	}

	type chunkAgg struct {
		score     float64
		chunk     elasticDriveChunkHit
		hasBM25   bool
		hasVector bool
	}
	type fileAgg struct {
		hit       elasticDriveFileHit
		score     float64
		chunks    map[int]*chunkAgg
		hasBM25   bool
		hasVector bool
	}

	const defaultRRFConstant = 60
	rrfConstant := s.cfg.RRFConstant
	if rrfConstant <= 0 {
		rrfConstant = defaultRRFConstant
	}

	files := make(map[string]*fileAgg)
	addList := func(hits []elasticDriveFileHit, isVector bool) {
		for rank, hit := range hits {
			fileID := strings.TrimSpace(hit.Source.FileID)
			if fileID == "" {
				continue
			}
			agg, ok := files[fileID]
			if !ok {
				agg = &fileAgg{
					hit:    elasticDriveFileHit{Source: hit.Source},
					chunks: make(map[int]*chunkAgg),
				}
				files[fileID] = agg
			}
			agg.score += 1.0 / float64(rrfConstant+rank+1)
			if isVector {
				agg.hasVector = true
			} else {
				agg.hasBM25 = true
			}
			if agg.hit.Source.FileID == "" {
				agg.hit.Source = hit.Source
			}
			for _, innerHit := range hit.InnerHits.TopChunksPerFile.Hits.Hits {
				cAgg, exists := agg.chunks[innerHit.Source.ChunkIndex]
				if !exists {
					cAgg = &chunkAgg{}
					agg.chunks[innerHit.Source.ChunkIndex] = cAgg
				}
				if isVector {
					cAgg.hasVector = true
				} else {
					cAgg.hasBM25 = true
				}
				if innerHit.Score > cAgg.score {
					cAgg.score = innerHit.Score
				}
				if len(innerHit.Highlight.Content) > 0 {
					cAgg.chunk.Highlight.Content = append([]string(nil), innerHit.Highlight.Content...)
				}
				cAgg.chunk.Score = cAgg.score
				cAgg.chunk.Source.ChunkIndex = innerHit.Source.ChunkIndex
				cAgg.chunk.Source.ChunkCount = innerHit.Source.ChunkCount
				cAgg.chunk.Source.Content = innerHit.Source.Content
			}
		}
	}

	addList(bm25Hits, false)
	addList(semanticHits, true)

	fileIDs := make([]string, 0, len(files))
	for fileID := range files {
		fileIDs = append(fileIDs, fileID)
	}
	sort.SliceStable(fileIDs, func(i, j int) bool {
		if files[fileIDs[i]].score == files[fileIDs[j]].score {
			return files[fileIDs[i]].hit.Source.UpdatedAt > files[fileIDs[j]].hit.Source.UpdatedAt
		}
		return files[fileIDs[i]].score > files[fileIDs[j]].score
	})

	merged := make([]elasticDriveFileHit, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		agg := files[fileID]
		agg.hit.Score = agg.score
		chunks := make([]int, 0, len(agg.chunks))
		for idx := range agg.chunks {
			chunks = append(chunks, idx)
		}
		sort.Ints(chunks)
		sort.SliceStable(chunks, func(i, j int) bool {
			if agg.chunks[chunks[i]].score == agg.chunks[chunks[j]].score {
				return chunks[i] < chunks[j]
			}
			return agg.chunks[chunks[i]].score > agg.chunks[chunks[j]].score
		})
		agg.hit.InnerHits.TopChunksPerFile.Hits.Hits = agg.hit.InnerHits.TopChunksPerFile.Hits.Hits[:0]
		for _, idx := range chunks {
			cAgg := agg.chunks[idx]
			agg.hit.InnerHits.TopChunksPerFile.Hits.Hits = append(agg.hit.InnerHits.TopChunksPerFile.Hits.Hits, elasticDriveChunkHit{
				Score: cAgg.score,
				Source: struct {
					ChunkIndex     int    `json:"chunk_index"`
					ChunkCount     int    `json:"chunk_count"`
					SectionIndex   int    `json:"section_index"`
					SectionID      string `json:"section_id"`
					SectionContent string `json:"section_content"`
					Content        string `json:"content"`
				}{
					ChunkIndex:     cAgg.chunk.Source.ChunkIndex,
					ChunkCount:     cAgg.chunk.Source.ChunkCount,
					SectionIndex:   cAgg.chunk.Source.SectionIndex,
					SectionID:      cAgg.chunk.Source.SectionID,
					SectionContent: cAgg.chunk.Source.SectionContent,
					Content:        cAgg.chunk.Source.Content,
				},
				Highlight: struct {
					Content []string `json:"content"`
				}{
					Content: append([]string(nil), cAgg.chunk.Highlight.Content...),
				},
			})
		}
		merged = append(merged, agg.hit)
	}

	return merged
}

func (s *DriveSearchService) rerankSearchHits(ctx context.Context, queryCtx *driveSearchQueryContext, hits []elasticDriveFileHit) ([]elasticDriveFileHit, error) {
	if !s.hybridEnabled() || s.embedder == nil || strings.TrimSpace(s.cfg.RerankModel) == "" {
		return nil, nil
	}

	type candidate struct {
		fileIndex  int
		chunkIndex int
	}

	candidates := make([]candidate, 0)
	for fileIndex := range hits {
		for chunkIndex := range hits[fileIndex].InnerHits.TopChunksPerFile.Hits.Hits {
			candidates = append(candidates, candidate{fileIndex: fileIndex, chunkIndex: chunkIndex})
		}
	}
	if len(candidates) == 0 {
		return hits, nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		ai := hits[candidates[i].fileIndex].InnerHits.TopChunksPerFile.Hits.Hits[candidates[i].chunkIndex].Score
		aj := hits[candidates[j].fileIndex].InnerHits.TopChunksPerFile.Hits.Hits[candidates[j].chunkIndex].Score
		if ai == aj {
			if candidates[i].fileIndex == candidates[j].fileIndex {
				return candidates[i].chunkIndex < candidates[j].chunkIndex
			}
			return candidates[i].fileIndex < candidates[j].fileIndex
		}
		return ai > aj
	})

	topN := s.cfg.RerankTopN
	if topN <= 0 {
		topN = 50
	}
	if topN > len(candidates) {
		topN = len(candidates)
	}
	candidates = candidates[:topN]

	documents := make([]string, len(candidates))
	for i, cand := range candidates {
		chunk := hits[cand.fileIndex].InnerHits.TopChunksPerFile.Hits.Hits[cand.chunkIndex]
		fileName := hits[cand.fileIndex].Source.FileName
		documents[i] = strings.TrimSpace(fileName + "\n" + chunk.Source.Content)
	}

	rerankQuery := strings.TrimSpace(queryCtx.RewrittenQuery)
	if rerankQuery == "" {
		rerankQuery = strings.TrimSpace(queryCtx.OriginalQuestion)
	}

	resp, err := s.embedder.Rerank(ctx, alem.RerankRequest{
		Model:     s.cfg.RerankModel,
		Query:     rerankQuery,
		Documents: documents,
		TopN:      topN,
	})
	if err != nil {
		return nil, fmt.Errorf("rerank search hits: %w", err)
	}
	if resp == nil || len(resp.Results) == 0 {
		return hits, nil
	}

	updated := make(map[int]map[int]float64)
	for _, result := range resp.Results {
		if result.Index < 0 || result.Index >= len(candidates) {
			continue
		}
		cand := candidates[result.Index]
		if _, ok := updated[cand.fileIndex]; !ok {
			updated[cand.fileIndex] = make(map[int]float64)
		}
		updated[cand.fileIndex][cand.chunkIndex] = result.RelevanceScore
	}

	for fileIndex, chunkScores := range updated {
		for chunkIndex, score := range chunkScores {
			hits[fileIndex].InnerHits.TopChunksPerFile.Hits.Hits[chunkIndex].Score = score
		}
	}

	for fileIndex := range hits {
		chunks := hits[fileIndex].InnerHits.TopChunksPerFile.Hits.Hits
		sort.SliceStable(chunks, func(i, j int) bool {
			if chunks[i].Score == chunks[j].Score {
				return chunks[i].Source.ChunkIndex < chunks[j].Source.ChunkIndex
			}
			return chunks[i].Score > chunks[j].Score
		})
		hits[fileIndex].InnerHits.TopChunksPerFile.Hits.Hits = chunks
		if len(chunks) > 0 {
			hits[fileIndex].Score = chunks[0].Score
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Source.UpdatedAt > hits[j].Source.UpdatedAt
		}
		return hits[i].Score > hits[j].Score
	})

	return hits, nil
}

func (s *DriveSearchService) shouldAttemptOnDemandIndex(fileIDs []string) bool {
	if len(fileIDs) == 0 {
		return false
	}
	return s.repo != nil && s.fileSvc != nil && s.extractor != nil
}

func (s *DriveSearchService) getIndexedFileIDs(ctx context.Context, fileIDs []string) (map[uuid.UUID]bool, error) {
	if !s.Enabled() || len(fileIDs) == 0 {
		return nil, nil
	}
	queryPayload := map[string]any{
		"size": 0,
		"query": map[string]any{
			"terms": map[string]any{
				"file_id": fileIDs,
			},
		},
		"aggs": map[string]any{
			"present_files": map[string]any{
				"terms": map[string]any{
					"field": "file_id",
					"size":  len(fileIDs) + 10,
				},
			},
		},
	}
	body, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, err
	}
	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_search"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return map[uuid.UUID]bool{}, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("indexed files check failed: status=%d body=%s", status, string(respBody))
	}

	var esResp struct {
		Aggregations struct {
			PresentFiles struct {
				Buckets []struct {
					Key string `json:"key"`
				} `json:"buckets"`
			} `json:"present_files"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(respBody, &esResp); err != nil {
		return nil, err
	}

	present := make(map[uuid.UUID]bool)
	for _, b := range esResp.Aggregations.PresentFiles.Buckets {
		if id, err := uuid.Parse(b.Key); err == nil {
			present[id] = true
		}
	}
	return present, nil
}

func (s *DriveSearchService) indexMissingFiles(ctx context.Context, fileIDs []string) (int, error) {
	if !s.shouldAttemptOnDemandIndex(fileIDs) {
		return 0, nil
	}

	present, err := s.getIndexedFileIDs(ctx, fileIDs)
	if err != nil {
		s.logger.Warn("failed to check indexed files in elasticsearch, attempting to index all", zap.Error(err))
		present = make(map[uuid.UUID]bool)
	}

	parsedIDs := make([]uuid.UUID, 0, len(fileIDs))
	seen := make(map[uuid.UUID]struct{}, len(fileIDs))
	for _, rawID := range fileIDs {
		if len(parsedIDs) >= maxOnDemandDriveSearchIndexFiles {
			break
		}
		fileID, err := uuid.Parse(strings.TrimSpace(rawID))
		if err != nil {
			continue
		}
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}

		if !present[fileID] {
			parsedIDs = append(parsedIDs, fileID)
		}
	}

	if len(parsedIDs) == 0 {
		return 0, nil
	}

	indexedCount := 0
	for _, fileID := range parsedIDs {
		s.logger.Info("auto-indexing missing file on search query", zap.String("file_id", fileID.String()))
		if err := s.IndexFile(ctx, fileID); err != nil {
			s.logger.Warn("failed to index file on search query", zap.String("file_id", fileID.String()), zap.Error(err))
			continue
		}
		indexedCount++
	}
	return indexedCount, nil
}

func (s *DriveSearchService) buildSearchShouldClauses(question string) []any {
	shouldClauses := []any{
		map[string]any{
			"match_phrase": map[string]any{
				"content": map[string]any{
					"query": question,
					"boost": 5,
				},
			},
		},
		map[string]any{
			"match": map[string]any{
				"content": map[string]any{
					"query":    question,
					"boost":    3,
					"operator": "and",
				},
			},
		},
		map[string]any{
			"multi_match": map[string]any{
				"query": question,
				"fields": []string{
					"content^4",
					"file_name^2",
					"folder_path",
				},
				"fuzziness": "AUTO",
			},
		},
	}

	if !isAnalyticalDriveQuestion(question) {
		return shouldClauses
	}

	shouldClauses = append(shouldClauses,
		map[string]any{
			"multi_match": map[string]any{
				"query":  question + " показатели метрики KPI отчет сводка таблица министерство ведомство",
				"fields": []string{"file_name^6", "folder_path^4", "content^3"},
				"type":   "best_fields",
				"boost":  4,
			},
		},
		map[string]any{
			"match_phrase": map[string]any{
				"file_name": map[string]any{
					"query": "министерство",
					"boost": 3,
				},
			},
		},
		map[string]any{
			"bool": map[string]any{
				"should": []any{
					map[string]any{"wildcard": map[string]any{"file_name.keyword": map[string]any{"value": "*.xlsx", "boost": 2.5}}},
					map[string]any{"wildcard": map[string]any{"file_name.keyword": map[string]any{"value": "*.xls", "boost": 2.3}}},
					map[string]any{"wildcard": map[string]any{"file_name.keyword": map[string]any{"value": "*.csv", "boost": 2.0}}},
				},
				"minimum_should_match": 1,
				"boost":                1.5,
			},
		},
	)
	return shouldClauses
}

func isAnalyticalDriveQuestion(question string) bool {
	normalized := strings.ToLower(strings.TrimSpace(question))
	if normalized == "" {
		return false
	}
	analyticalMarkers := []string{
		"показател", "метрик", "kpi", "отчет", "отчёт", "аналит", "статист", "сводк",
		"динами", "результат", "таблиц", "xlsx", "xls", "csv", "көрсеткіш", "есеп", "талдау",
		"министер", "ведомств", "министрлік",
	}
	for _, marker := range analyticalMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func (s *DriveSearchService) loadVisibleSearchFiles(ctx context.Context, userID int64, hits []elasticDriveFileHit) (map[uuid.UUID]*domain.DriveFile, map[uuid.UUID]struct{}, error) {
	if s.repo == nil {
		return nil, nil, fmt.Errorf("drive search access repository is required")
	}

	fileIDs := make([]uuid.UUID, 0, len(hits))
	seen := make(map[uuid.UUID]struct{}, len(hits))
	for _, hit := range hits {
		fileID, err := uuid.Parse(strings.TrimSpace(hit.Source.FileID))
		if err != nil {
			continue
		}
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}
		fileIDs = append(fileIDs, fileID)
	}
	if len(fileIDs) == 0 {
		return map[uuid.UUID]*domain.DriveFile{}, map[uuid.UUID]struct{}{}, nil
	}

	files, err := s.repo.GetActiveDriveFilesByIDs(ctx, fileIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load active drive files: %w", err)
	}
	fileMetaByID := make(map[uuid.UUID]*domain.DriveFile, len(files))
	activeIDs := make([]uuid.UUID, 0, len(files))
	for _, file := range files {
		fileMetaByID[file.ID] = file
		activeIDs = append(activeIDs, file.ID)
	}
	if len(activeIDs) == 0 {
		return fileMetaByID, map[uuid.UUID]struct{}{}, nil
	}

	accessibleIDs, err := s.repo.ListAccessibleDriveFileIDs(ctx, userID, activeIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load accessible drive file ids: %w", err)
	}
	accessibleByID := make(map[uuid.UUID]struct{}, len(accessibleIDs))
	for _, id := range accessibleIDs {
		accessibleByID[id] = struct{}{}
	}

	return fileMetaByID, accessibleByID, nil
}

func (s *DriveSearchService) IndexFileAsync(fileID uuid.UUID) {
	if s.local != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.local.Enqueue(ctx, fileID); err != nil {
			s.logger.Warn("file index enqueue failed; worker will reconcile", zap.Error(err))
		}
		return
	}
	if !s.Enabled() || s.repo == nil || s.fileSvc == nil || s.extractor == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.indexTimeout())
		defer cancel()

		if err := s.IndexFile(ctx, fileID); err != nil {
			s.logger.Warn("failed to sync drive file to elasticsearch",
				zap.String("file_id", fileID.String()),
				zap.Error(err),
			)
		}
	}()
}

func (s *DriveSearchService) DeleteFileAsync(fileID uuid.UUID) {
	if !s.Enabled() {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.indexTimeout())
		defer cancel()

		if err := s.DeleteFile(ctx, fileID); err != nil {
			s.logger.Warn("failed to delete drive file from elasticsearch",
				zap.String("file_id", fileID.String()),
				zap.Error(err),
			)
		}
	}()
}

func (s *DriveSearchService) IndexFile(ctx context.Context, fileID uuid.UUID) error {
	if s.local != nil {
		return s.local.Enqueue(ctx, fileID)
	}
	if !s.Enabled() {
		return fmt.Errorf("drive search is not configured")
	}
	if s.repo == nil || s.fileSvc == nil || s.extractor == nil {
		return fmt.Errorf("drive search index dependencies are not configured")
	}

	file, err := s.repo.GetFileByID(ctx, fileID)
	if err != nil || file == nil || file.DeletedAt != nil || file.Scope != "drive" {
		if deleteErr := s.DeleteFile(ctx, fileID); deleteErr != nil {
			return deleteErr
		}
		return err
	}

	maxBytes := s.cfg.MaxFileSizeBytes
	if maxBytes <= 0 {
		maxBytes = 50 * 1024 * 1024
	}
	if file.Size > maxBytes {
		_ = s.DeleteFile(ctx, fileID)
		return fmt.Errorf("drive file is too large for indexing: size=%d max=%d", file.Size, maxBytes)
	}

	reader, _, _, err := s.fileSvc.DownloadFile(ctx, file.ObjectName)
	if err != nil {
		return fmt.Errorf("download drive file: %w", err)
	}
	defer reader.Close()

	buf, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read drive file: %w", err)
	}
	if int64(len(buf)) > maxBytes {
		_ = s.DeleteFile(ctx, fileID)
		return fmt.Errorf("drive file exceeded indexing read limit: size>%d", maxBytes)
	}

	text, err := extractFileText(ctx, s.extractor, file.RealName, buf)
	if err != nil {
		if errors.Is(err, errNoExtractableText) || strings.Contains(err.Error(), "unsupported file format") {
			_ = s.DeleteFile(ctx, fileID)
		}
		return fmt.Errorf("extract drive file text: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		_ = s.DeleteFile(ctx, fileID)
		return nil
	}

	chunks := buildDriveSearchIndexedChunks(file, text, s.cfg.ChunkSize, s.cfg.ChunkOverlap)
	if len(chunks) == 0 {
		_ = s.DeleteFile(ctx, fileID)
		return nil
	}

	users, err := s.repo.ListUsersWithAccess(ctx, domain.ResourceFile, fileID)
	if err != nil {
		return fmt.Errorf("load drive file ACL: %w", err)
	}
	allowedUserIDs := make([]int64, 0, len(users)+1)
	seen := make(map[int64]struct{}, len(users)+1)
	for _, user := range users {
		if user.ID <= 0 {
			continue
		}
		if _, ok := seen[user.ID]; ok {
			continue
		}
		seen[user.ID] = struct{}{}
		allowedUserIDs = append(allowedUserIDs, user.ID)
	}
	if _, ok := seen[file.OwnerID]; !ok && file.OwnerID > 0 {
		allowedUserIDs = append(allowedUserIDs, file.OwnerID)
	}

	if err := s.ensureIndex(ctx); err != nil {
		return err
	}
	if err := s.DeleteFile(ctx, fileID); err != nil {
		return err
	}

	return s.bulkIndexFileChunks(ctx, file, allowedUserIDs, chunks)
}

func (s *DriveSearchService) DeleteFile(ctx context.Context, fileID uuid.UUID) error {
	if s.local != nil {
		return s.local.Delete(ctx, []uuid.UUID{fileID})
	}
	if !s.Enabled() {
		return fmt.Errorf("drive search is not configured")
	}

	payload := map[string]any{
		"query": map[string]any{
			"term": map[string]any{
				"file_id": fileID.String(),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal elastic delete query: %w", err)
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_delete_by_query?conflicts=proceed&refresh=true"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elastic delete failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (s *DriveSearchService) UpdateAllowedUsers(ctx context.Context, fileID uuid.UUID) error {
	if s.local != nil {
		return nil
	} // Local retrieval checks current ACLs on every read.
	if !s.Enabled() {
		return nil
	}
	if s.repo == nil {
		return fmt.Errorf("drive search index dependencies are not configured")
	}

	file, err := s.repo.GetFileByID(ctx, fileID)
	if err != nil || file == nil || file.DeletedAt != nil || file.Scope != "drive" {
		_ = s.DeleteFile(ctx, fileID)
		return err
	}

	users, err := s.repo.ListUsersWithAccess(ctx, domain.ResourceFile, fileID)
	if err != nil {
		return fmt.Errorf("load drive file ACL: %w", err)
	}

	allowedUserIDs := make([]int64, 0, len(users)+1)
	seen := make(map[int64]struct{}, len(users)+1)
	for _, user := range users {
		if user.ID <= 0 {
			continue
		}
		if _, ok := seen[user.ID]; ok {
			continue
		}
		seen[user.ID] = struct{}{}
		allowedUserIDs = append(allowedUserIDs, user.ID)
	}
	if _, ok := seen[file.OwnerID]; !ok && file.OwnerID > 0 {
		allowedUserIDs = append(allowedUserIDs, file.OwnerID)
	}

	payload := map[string]any{
		"script": map[string]any{
			"source": "ctx._source.allowed_user_ids = params.allowed_user_ids",
			"lang":   "painless",
			"params": map[string]any{
				"allowed_user_ids": allowedUserIDs,
			},
		},
		"query": map[string]any{
			"term": map[string]any{
				"file_id": fileID.String(),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal elastic update query: %w", err)
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_update_by_query?conflicts=proceed&refresh=true"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elastic update allowed users failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (s *DriveSearchService) DeleteFilesBulk(ctx context.Context, fileIDs []uuid.UUID) error {
	if s.local != nil {
		return s.local.Delete(ctx, fileIDs)
	}
	if !s.Enabled() {
		return nil
	}
	if len(fileIDs) == 0 {
		return nil
	}

	strIDs := make([]string, len(fileIDs))
	for i, id := range fileIDs {
		strIDs[i] = id.String()
	}

	payload := map[string]any{
		"query": map[string]any{
			"terms": map[string]any{
				"file_id": strIDs,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal elastic bulk delete query: %w", err)
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_delete_by_query?conflicts=proceed&refresh=true"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elastic bulk delete failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (s *DriveSearchService) DeleteFilesBulkAsync(fileIDs []uuid.UUID) {
	if !s.Enabled() || len(fileIDs) == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.indexTimeout())
		defer cancel()

		if err := s.DeleteFilesBulk(ctx, fileIDs); err != nil {
			s.logger.Warn("failed to delete drive files from elasticsearch in bulk",
				zap.Int("count", len(fileIDs)),
				zap.Error(err),
			)
		}
	}()
}

func (s *DriveSearchService) ensureIndex(ctx context.Context) error {
	path := "/" + strings.Trim(s.cfg.Index, "/")
	_, status, err := s.elasticRequest(ctx, http.MethodHead, path, nil, "")
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return s.ensureHybridMapping(ctx)
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("elastic index check failed: status=%d", status)
	}

	mapping := s.buildIndexMapping()
	body, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("marshal elastic index mapping: %w", err)
	}
	respBody, status, err := s.elasticRequest(ctx, http.MethodPut, path, body, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elastic index create failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}
	return s.ensureHybridMapping(ctx)
}

type driveSearchIndexedChunk struct {
	ChunkIndex        int
	ChunkCount        int
	SectionIndex      int
	SectionChunkIndex int
	SectionChunkCount int
	SectionID         string
	SectionContent    string
	Content           string
}

func (s *DriveSearchService) bulkIndexFileChunks(ctx context.Context, file *domain.DriveFile, allowedUserIDs []int64, chunks []driveSearchIndexedChunk) error {
	var bulk bytes.Buffer
	encoder := json.NewEncoder(&bulk)
	contents := make([]string, len(chunks))
	for i := range chunks {
		contents[i] = chunks[i].Content
	}
	vectors, err := s.embedChunks(ctx, contents)
	if err != nil {
		s.logger.Warn("drive chunk embedding failed, indexing lexical content only",
			zap.String("file_id", file.ID.String()),
			zap.Error(err),
		)
	}

	seenSections := make(map[string]driveSearchIndexedChunk)
	for _, chunk := range chunks {
		if _, ok := seenSections[chunk.SectionID]; !ok {
			seenSections[chunk.SectionID] = chunk
		}
	}
	sectionIDs := make([]string, 0, len(seenSections))
	for sectionID := range seenSections {
		sectionIDs = append(sectionIDs, sectionID)
	}
	sort.Strings(sectionIDs)

	for _, sectionID := range sectionIDs {
		sectionChunk := seenSections[sectionID]
		parentAction := map[string]any{
			"index": map[string]any{
				"_id": sectionChunk.SectionID,
			},
		}
		if err := encoder.Encode(parentAction); err != nil {
			return fmt.Errorf("encode elastic parent action: %w", err)
		}
		parentDoc := map[string]any{
			"file_id":          file.ID.String(),
			"file_name":        file.RealName,
			"folder_path":      "",
			"owner_id":         file.OwnerID,
			"allowed_user_ids": allowedUserIDs,
			"is_public":        file.IsPublic,
			"mime_type":        file.MimeType,
			"size":             file.Size,
			"version":          file.Version,
			"doc_kind":         "section_parent",
			"section_index":    sectionChunk.SectionIndex,
			"section_id":       sectionChunk.SectionID,
			"section_content":  sectionChunk.SectionContent,
			"chunk_count":      sectionChunk.ChunkCount,
			"created_at":       file.CreatedAt.Format(time.RFC3339Nano),
			"updated_at":       file.UpdatedAt.Format(time.RFC3339Nano),
		}
		if err := encoder.Encode(parentDoc); err != nil {
			return fmt.Errorf("encode elastic parent document: %w", err)
		}
	}

	for i, chunk := range chunks {
		action := map[string]any{
			"index": map[string]any{
				"_id":      fmt.Sprintf("%s:%d:%d:%d", file.ID.String(), file.Version, chunk.SectionIndex, chunk.ChunkIndex),
				"_routing": chunk.SectionID,
			},
		}
		if err := encoder.Encode(action); err != nil {
			return fmt.Errorf("encode elastic bulk action: %w", err)
		}
		doc := map[string]any{
			"file_id":             file.ID.String(),
			"file_name":           file.RealName,
			"folder_path":         "",
			"owner_id":            file.OwnerID,
			"allowed_user_ids":    allowedUserIDs,
			"is_public":           file.IsPublic,
			"mime_type":           file.MimeType,
			"size":                file.Size,
			"version":             file.Version,
			"doc_kind":            "section_chunk",
			"chunk_index":         chunk.ChunkIndex,
			"chunk_count":         chunk.ChunkCount,
			"section_index":       chunk.SectionIndex,
			"section_id":          chunk.SectionID,
			"section_chunk_index": chunk.SectionChunkIndex,
			"section_chunk_count": chunk.SectionChunkCount,
			"section_content":     chunk.SectionContent,
			"content":             chunk.Content,
			"created_at":          file.CreatedAt.Format(time.RFC3339Nano),
			"updated_at":          file.UpdatedAt.Format(time.RFC3339Nano),
		}
		if len(vectors) > i {
			doc[s.vectorField()] = vectors[i]
		}
		if err := encoder.Encode(doc); err != nil {
			return fmt.Errorf("encode elastic bulk document: %w", err)
		}
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_bulk?refresh=true"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPost, path, bulk.Bytes(), "application/x-ndjson")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elastic bulk index failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}

	var bulkResp struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &bulkResp); err != nil {
		return fmt.Errorf("decode elastic bulk response: %w", err)
	}
	if bulkResp.Errors {
		return fmt.Errorf("elastic bulk index returned item errors: body=%s", strings.TrimSpace(string(respBody)))
	}

	return nil
}

func (s *DriveSearchService) ensureHybridMapping(ctx context.Context) error {
	mapping := map[string]any{
		"properties": s.buildDriveSearchProperties(),
	}
	body, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("marshal hybrid mapping: %w", err)
	}

	path := "/" + strings.Trim(s.cfg.Index, "/") + "/_mapping"
	respBody, status, err := s.elasticRequest(ctx, http.MethodPut, path, body, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elastic hybrid mapping update failed: status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (s *DriveSearchService) buildIndexMapping() map[string]any {
	return map[string]any{
		"mappings": map[string]any{
			"properties": s.buildDriveSearchProperties(),
		},
	}
}

func (s *DriveSearchService) buildDriveSearchProperties() map[string]any {
	properties := map[string]any{
		"file_id":             map[string]any{"type": "keyword"},
		"file_name":           map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword"}}},
		"folder_path":         map[string]any{"type": "text"},
		"owner_id":            map[string]any{"type": "long"},
		"allowed_user_ids":    map[string]any{"type": "long"},
		"is_public":           map[string]any{"type": "boolean"},
		"mime_type":           map[string]any{"type": "keyword"},
		"size":                map[string]any{"type": "long"},
		"version":             map[string]any{"type": "integer"},
		"doc_kind":            map[string]any{"type": "keyword"},
		"chunk_index":         map[string]any{"type": "integer"},
		"chunk_count":         map[string]any{"type": "integer"},
		"section_index":       map[string]any{"type": "integer"},
		"section_id":          map[string]any{"type": "keyword"},
		"section_chunk_index": map[string]any{"type": "integer"},
		"section_chunk_count": map[string]any{"type": "integer"},
		"section_content":     map[string]any{"type": "text"},
		"content":             map[string]any{"type": "text"},
		"drive_relation": map[string]any{
			"type": "join",
			"relations": map[string]any{
				"section_parent": "section_chunk",
			},
		},
		"created_at": map[string]any{"type": "date"},
		"updated_at": map[string]any{"type": "date"},
	}
	if s.hybridEnabled() {
		properties[s.vectorField()] = map[string]any{
			"type":       "dense_vector",
			"dims":       s.cfg.EmbeddingDims,
			"index":      true,
			"similarity": s.vectorSimilarity(),
		}
	}
	return properties
}

func (s *DriveSearchService) hybridEnabled() bool {
	return strings.TrimSpace(s.cfg.EmbeddingModel) != "" && s.cfg.EmbeddingDims > 0
}

func (s *DriveSearchService) vectorField() string {
	if field := strings.TrimSpace(s.cfg.VectorField); field != "" {
		return field
	}
	return "content_vector"
}

func (s *DriveSearchService) vectorSimilarity() string {
	switch strings.ToLower(strings.TrimSpace(s.cfg.VectorSimilarity)) {
	case "dot_product", "l2_norm", "max_inner_product", "cosine":
		return strings.ToLower(strings.TrimSpace(s.cfg.VectorSimilarity))
	default:
		return "cosine"
	}
}

func (s *DriveSearchService) queryRewriteEnabled() bool {
	return strings.TrimSpace(s.cfg.QueryRewriteModel) != "" || strings.TrimSpace(s.cfg.HydeModel) != ""
}

func (s *DriveSearchService) queryRewriteModel() string {
	if model := strings.TrimSpace(s.cfg.QueryRewriteModel); model != "" {
		return model
	}
	return strings.TrimSpace(s.cfg.HydeModel)
}

func (s *DriveSearchService) hydeModel() string {
	if model := strings.TrimSpace(s.cfg.HydeModel); model != "" {
		return model
	}
	return strings.TrimSpace(s.cfg.QueryRewriteModel)
}

func (s *DriveSearchService) queryRewriteMaxTokens() int {
	if s.cfg.QueryRewriteMaxTokens > 0 {
		return s.cfg.QueryRewriteMaxTokens
	}
	return 96
}

func (s *DriveSearchService) hydeMaxTokens() int {
	if s.cfg.HydeMaxTokens > 0 {
		return s.cfg.HydeMaxTokens
	}
	return 160
}

func extractChatContentText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", content))
	}
}

func decodeSearchRewriteJSON(content string, target any) error {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return fmt.Errorf("empty rewrite response")
	}
	return json.Unmarshal([]byte(trimmed), target)
}

func (s *DriveSearchService) embedChunks(ctx context.Context, chunks []string) ([][]float64, error) {
	if !s.hybridEnabled() || s.embedder == nil || len(chunks) == 0 {
		return nil, nil
	}
	return s.embedTexts(ctx, chunks)
}

func (s *DriveSearchService) embedTexts(ctx context.Context, inputs []string) ([][]float64, error) {
	resp, err := s.embedder.CreateEmbeddings(ctx, alem.EmbeddingRequest{
		Model: s.cfg.EmbeddingModel,
		Input: inputs,
	})
	if err != nil {
		return nil, fmt.Errorf("create embeddings: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("embedding response is nil")
	}
	if len(resp.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding count mismatch: got=%d want=%d", len(resp.Data), len(inputs))
	}

	vectors := make([][]float64, len(resp.Data))
	for _, item := range resp.Data {
		if item.Index < 0 || item.Index >= len(inputs) {
			return nil, fmt.Errorf("embedding index out of range: %d", item.Index)
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}

func (s *DriveSearchService) elasticRequest(ctx context.Context, method, path string, body []byte, contentType string) ([]byte, int, error) {
	url := strings.TrimRight(s.cfg.ElasticsearchURL, "/") + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("create elastic request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("perform elastic request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read elastic response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

func (s *DriveSearchService) indexTimeout() time.Duration {
	timeout := s.cfg.RequestTimeout * 3
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	return timeout
}

func buildDriveSearchIndexedChunks(file *domain.DriveFile, text string, chunkSize, overlap int) []driveSearchIndexedChunk {
	sections := splitDriveSearchSections(text)
	if len(sections) == 0 {
		return nil
	}

	chunks := make([]driveSearchIndexedChunk, 0)
	globalIndex := 0
	for sectionIndex, section := range sections {
		sectionChunks := splitDriveSearchChunks(section, chunkSize, overlap)
		if len(sectionChunks) == 0 {
			continue
		}

		sectionID := fmt.Sprintf("%s:%d:section:%d", file.ID.String(), file.Version, sectionIndex)
		for sectionChunkIndex, content := range sectionChunks {
			chunks = append(chunks, driveSearchIndexedChunk{
				ChunkIndex:        globalIndex,
				ChunkCount:        0,
				SectionIndex:      sectionIndex,
				SectionChunkIndex: sectionChunkIndex,
				SectionChunkCount: len(sectionChunks),
				SectionID:         sectionID,
				SectionContent:    section,
				Content:           content,
			})
			globalIndex++
		}
	}

	totalChunks := len(chunks)
	for i := range chunks {
		chunks[i].ChunkCount = totalChunks
	}
	return chunks
}

func splitDriveSearchSections(text string) []string {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" {
		return nil
	}

	lines := strings.Split(normalized, "\n")
	sections := make([]string, 0, len(lines))
	current := make([]string, 0, len(lines))
	flush := func() {
		if len(current) == 0 {
			return
		}
		section := strings.TrimSpace(strings.Join(current, "\n"))
		if section != "" {
			sections = append(sections, section)
		}
		current = current[:0]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()

	if len(sections) == 0 {
		return []string{normalized}
	}
	return sections
}

func splitDriveSearchChunks(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 1200
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 4
	}

	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}

	chunks := make([]string, 0, (len(runes)/chunkSize)+1)
	for start := 0; start < len(runes); {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}

		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}

		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return chunks
}

type elasticDriveSearchResponse struct {
	Hits struct {
		Hits []elasticDriveFileHit `json:"hits"`
	} `json:"hits"`
}

type elasticDriveFileHit struct {
	Score  float64 `json:"_score"`
	Source struct {
		FileID     string `json:"file_id"`
		FileName   string `json:"file_name"`
		FolderPath string `json:"folder_path"`
		UpdatedAt  string `json:"updated_at"`
	} `json:"_source"`
	InnerHits struct {
		TopChunksPerFile struct {
			Hits struct {
				Hits []elasticDriveChunkHit `json:"hits"`
			} `json:"hits"`
		} `json:"top_chunks_per_file"`
	} `json:"inner_hits"`
}

type elasticDriveChunkHit struct {
	Score  float64 `json:"_score"`
	Source struct {
		ChunkIndex     int    `json:"chunk_index"`
		ChunkCount     int    `json:"chunk_count"`
		SectionIndex   int    `json:"section_index"`
		SectionID      string `json:"section_id"`
		SectionContent string `json:"section_content"`
		Content        string `json:"content"`
	} `json:"_source"`
	Highlight struct {
		Content []string `json:"content"`
	} `json:"highlight"`
}

type elasticDriveChunkContextResponse struct {
	Hits struct {
		Hits []elasticDriveChunkContextHit `json:"hits"`
	} `json:"hits"`
}

type elasticDriveChunkContextHit struct {
	Score  float64 `json:"_score"`
	Source struct {
		ChunkIndex     int    `json:"chunk_index"`
		ChunkCount     int    `json:"chunk_count"`
		SectionIndex   int    `json:"section_index"`
		SectionID      string `json:"section_id"`
		SectionContent string `json:"section_content"`
		Content        string `json:"content"`
	} `json:"_source"`
}

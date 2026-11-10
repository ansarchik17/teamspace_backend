package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/west6ide/workspace-backend/internal/documentsearch"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const (
	AIDocumentMaxBytes    int64 = 50 * 1024 * 1024
	AIDocumentWorkerLease       = 2 * time.Minute
	AIDocumentMaxAttempts       = 3
	AIDocumentChunkChars        = 3000
)

var allowedAIDocumentExtensions = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".txt":  "text/plain",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

type AIDocumentService struct {
	repo        *repository.AIDocumentRepository
	aiRepo      *repository.AIRepository
	fileSvc     *FileService
	extractor   *ContentExtractionService
	logger      *zap.Logger
	work        chan struct{}
	wake        chan struct{}
	startOnce   sync.Once
	fileIndexer interface{ WorkOnce(context.Context) error }
}

func NewAIDocumentService(repo *repository.AIDocumentRepository, aiRepo *repository.AIRepository, fileSvc *FileService, extractor *ContentExtractionService, logger *zap.Logger) *AIDocumentService {
	return &AIDocumentService{
		repo: repo, aiRepo: aiRepo, fileSvc: fileSvc, extractor: extractor, logger: logger.With(zap.String("service", "ai_documents")),
		work: make(chan struct{}, 2), wake: make(chan struct{}, 1),
	}
}

func (s *AIDocumentService) Create(ctx context.Context, userID int64, conversationID string, file *multipart.FileHeader) (*repository.AIDocument, error) {
	if file == nil {
		return nil, fmt.Errorf("file is required")
	}
	if file.Size <= 0 || file.Size > AIDocumentMaxBytes {
		return nil, fmt.Errorf("file size must be between 1 byte and 50 MB")
	}
	name := filepath.Base(strings.TrimSpace(file.Filename))
	ext := strings.ToLower(filepath.Ext(name))
	detectedType, ok := allowedAIDocumentExtensions[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported document format: %s", ext)
	}
	chat, err := s.aiRepo.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("check chat: %w", err)
	}
	if chat == nil || chat.UserID != userID {
		return nil, fmt.Errorf("unauthorized: conversation does not belong to user")
	}
	active, err := s.repo.CountActive(ctx, userID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("check document quota: %w", err)
	}
	if active >= 20 {
		return nil, fmt.Errorf("document quota reached for this chat")
	}

	fileReader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	header := make([]byte, 8)
	readHeader, headerErr := io.ReadFull(fileReader, header)
	_ = fileReader.Close()
	if headerErr != nil && headerErr != io.ErrUnexpectedEOF && headerErr != io.EOF {
		return nil, fmt.Errorf("read file header: %w", headerErr)
	}
	if !validAIDocumentSignature(ext, header[:readHeader]) {
		return nil, fmt.Errorf("file content does not match its extension")
	}

	fileReader, err = file.Open()
	if err != nil {
		return nil, fmt.Errorf("reopen file: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.CopyN(hash, fileReader, file.Size)
	_ = fileReader.Close()
	if copyErr != nil && copyErr != io.EOF {
		return nil, fmt.Errorf("hash file: %w", copyErr)
	}
	sha := hex.EncodeToString(hash.Sum(nil))
	if existing, err := s.repo.GetDocumentBySHA256(ctx, userID, conversationID, sha); err != nil {
		return nil, err
	} else if existing != nil {
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return localizedAIDocument(ctx, existing), nil
	}

	fileReader, err = file.Open()
	if err != nil {
		return nil, fmt.Errorf("reopen file: %w", err)
	}
	objectKey, err := s.fileSvc.UploadFile(ctx, userID, name, "ai-documents", file.Size, fileReader)
	_ = fileReader.Close()
	if err != nil {
		return nil, fmt.Errorf("store document: %w", err)
	}

	doc := repository.AIDocument{
		ConversationID: conversationID, OwnerID: userID, Name: name, DeclaredMIME: aiDocumentOptionalString(file.Header.Get("Content-Type")),
		DetectedType: detectedType, Bytes: file.Size, SHA256: sha, ObjectKey: objectKey, Status: "uploaded",
		ParserVersion: "v1", IndexVersion: "retrieval-v2",
	}
	id, err := s.repo.CreateDocument(ctx, doc)
	if err != nil {
		_ = s.fileSvc.DeleteFile(ctx, objectKey)
		return nil, err
	}
	created, err := s.repo.GetDocument(ctx, userID, conversationID, id)
	if err != nil {
		return nil, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return localizedAIDocument(ctx, created), nil
}

func (s *AIDocumentService) List(ctx context.Context, userID int64, conversationID string) ([]repository.AIDocument, error) {
	if err := s.ensureOwner(ctx, userID, conversationID); err != nil {
		return nil, err
	}
	docs, err := s.repo.ListDocuments(ctx, userID, conversationID)
	for i := range docs {
		docs[i] = *localizedAIDocument(ctx, &docs[i])
	}
	return docs, err
}

func (s *AIDocumentService) Get(ctx context.Context, userID int64, conversationID, documentID string) (*repository.AIDocument, error) {
	if err := s.ensureOwner(ctx, userID, conversationID); err != nil {
		return nil, err
	}
	doc, err := s.repo.GetDocument(ctx, userID, conversationID, documentID)
	return localizedAIDocument(ctx, doc), err
}

func (s *AIDocumentService) Delete(ctx context.Context, userID int64, conversationID, documentID string) error {
	if err := s.ensureOwner(ctx, userID, conversationID); err != nil {
		return err
	}
	doc, err := s.repo.GetDocument(ctx, userID, conversationID, documentID)
	if err != nil || doc == nil {
		return fmt.Errorf("document not found")
	}
	if err := s.repo.DeleteDocument(ctx, userID, conversationID, documentID); err != nil {
		return err
	}
	if doc.ObjectKey != "" {
		_ = s.fileSvc.DeleteFile(ctx, doc.ObjectKey)
	}
	return nil
}

func (s *AIDocumentService) DownloadURL(ctx context.Context, userID int64, conversationID, documentID string) (string, error) {
	if err := s.ensureOwner(ctx, userID, conversationID); err != nil {
		return "", err
	}
	doc, err := s.repo.GetDocument(ctx, userID, conversationID, documentID)
	if err != nil || doc == nil || doc.Status == "deleted" {
		return "", fmt.Errorf("document not found")
	}
	return s.fileSvc.GetFileURL(ctx, doc.ObjectKey, 10*time.Minute)
}

func (s *AIDocumentService) Retry(ctx context.Context, userID int64, conversationID, documentID string) error {
	if err := s.ensureOwner(ctx, userID, conversationID); err != nil {
		return err
	}
	if err := s.repo.RetryDocument(ctx, userID, conversationID, documentID); err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *AIDocumentService) SetFileIndexer(indexer interface{ WorkOnce(context.Context) error }) {
	s.fileIndexer = indexer
}

func (s *AIDocumentService) Start(ctx context.Context) {
	s.startOnce.Do(func() { go s.run(ctx) })
}

func (s *AIDocumentService) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(2 * time.Second):
		}
		embeddingCtx, cancelEmbeddings := context.WithTimeout(ctx, 45*time.Second)
		if err := s.repo.BackfillEmbeddings(embeddingCtx); err != nil {
			s.logger.Warn("document embedding backfill failed", zap.Error(err))
		}
		cancelEmbeddings()
		// Reserve capacity before claiming a lease. A queued job must not lose
		// its attempts while waiting for one of the two extraction slots.
	claimBatch:
		for dispatched := 0; dispatched < cap(s.work); dispatched++ {
			select {
			case <-ctx.Done():
				return
			case s.work <- struct{}{}:
			default:
				break claimBatch
			}
			doc, jobID, err := s.repo.ClaimNextJob(ctx, AIDocumentWorkerLease)
			if err != nil {
				<-s.work
				s.logger.Error("claim document job failed", zap.Error(err))
				break
			}
			if doc == nil {
				<-s.work
				break
			}
			go func(doc *repository.AIDocument, jobID string) {
				defer func() { <-s.work }()
				s.process(ctx, doc, jobID)
			}(doc, jobID)
		}

		if s.fileIndexer != nil {
			fileCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			if err := s.fileIndexer.WorkOnce(fileCtx); err != nil {
				s.logger.Warn("file index maintenance failed", zap.Error(err))
			}
			cancel()
		}
	}
}

func (s *AIDocumentService) process(ctx context.Context, doc *repository.AIDocument, jobID string) {
	ctx, cancel := context.WithTimeout(ctx, AIDocumentWorkerLease-10*time.Second)
	defer cancel()
	reader, _, size, err := s.fileSvc.DownloadFile(ctx, doc.ObjectKey)
	if err == nil && (size <= 0 || size > AIDocumentMaxBytes) {
		err = fmt.Errorf("stored document size is invalid")
	}
	if err != nil {
		s.fail(ctx, jobID, doc, "storage_error", err)
		return
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, AIDocumentMaxBytes+1))
	_ = reader.Close()
	if readErr != nil {
		s.fail(ctx, jobID, doc, "read_error", readErr)
		return
	}
	if int64(len(data)) > AIDocumentMaxBytes {
		s.fail(ctx, jobID, doc, "file_too_large", fmt.Errorf("file exceeds 50 MB"))
		return
	}
	text, err := s.extractor.ExtractTextBytesContext(ctx, doc.Name, data)
	if err != nil || strings.TrimSpace(text) == "" {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			s.fail(ctx, jobID, doc, "processing_interrupted", err)
			return
		}
		if err == nil {
			err = fmt.Errorf("no readable text found")
		}
		s.fail(ctx, jobID, doc, "text_not_found", err)
		return
	}
	chunks := makeDocumentChunks(doc, text)
	doc.TextChars = len([]rune(text))
	doc.IndexVersion = "retrieval-v2"
	if err := s.repo.MarkReady(ctx, jobID, *doc, chunks); err != nil {
		if errors.Is(err, repository.ErrAIDocumentLeaseLost) {
			s.logger.Info("document attempt superseded", zap.String("document_id", doc.ID))
			return
		}
		s.fail(ctx, jobID, doc, "index_error", err)
		return
	}
	s.logger.Info("AI document indexed", zap.String("document_id", doc.ID), zap.Int("chunks", len(chunks)))
}

func (s *AIDocumentService) fail(ctx context.Context, jobID string, doc *repository.AIDocument, code string, err error) {
	s.logger.Warn("AI document processing failed", zap.String("document_id", doc.ID), zap.String("error_code", code), zap.Error(err))
	retry := code != "file_too_large" && code != "text_not_found"
	message := "Документ не удалось обработать. Попробуйте повторить загрузку."
	switch code {
	case "file_too_large":
		message = "Документ превышает допустимый размер."
	case "text_not_found":
		message = "В документе не найден читаемый текст. Для скана нужен OCR."
	case "storage_error":
		message = "Не удалось прочитать файл из хранилища."
	case "read_error":
		message = "Не удалось прочитать содержимое документа."
	case "index_error":
		message = "Не удалось подготовить поиск по документу."
	case "processing_interrupted":
		message = "Обработка документа прервана. Повторите попытку, если она не возобновится автоматически."
	}
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if markErr := s.repo.MarkFailed(failureCtx, jobID, doc.ID, code, message, retry, doc.JobAttempt); markErr != nil {
		if errors.Is(markErr, repository.ErrAIDocumentLeaseLost) {
			s.logger.Info("failed document attempt superseded", zap.String("document_id", doc.ID))
			return
		}
		s.logger.Error("mark document failed", zap.Error(markErr))
	}
}

func (s *AIDocumentService) ensureOwner(ctx context.Context, userID int64, conversationID string) error {
	chat, err := s.aiRepo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if chat == nil || chat.UserID != userID {
		return fmt.Errorf("unauthorized: conversation does not belong to user")
	}
	return nil
}

func makeDocumentChunks(doc *repository.AIDocument, text string) []repository.AIDocumentChunk {
	windows := documentsearch.Split(text, AIDocumentChunkChars, 300)
	chunks := make([]repository.AIDocumentChunk, 0, len(windows))
	for _, window := range windows {
		chunks = append(chunks, repository.AIDocumentChunk{DocumentID: doc.ID, ConversationID: doc.ConversationID, Name: doc.Name, Ordinal: window.Ordinal, Text: window.Text,
			Locator: map[string]any{"chunk": window.Ordinal + 1, "start_line": window.StartLine, "end_line": window.EndLine, "start_char": window.StartChar, "end_char": window.EndChar}, TokenCount: len(strings.Fields(window.Text)),
		})
	}
	return chunks
}

func aiDocumentOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func validAIDocumentSignature(ext string, header []byte) bool {
	if ext == ".txt" {
		return true
	}
	if ext == ".pdf" {
		return len(header) >= 5 && string(header[:5]) == "%PDF-"
	}
	return len(header) >= 4 && header[0] == 'P' && header[1] == 'K' && header[2] == 3 && header[3] == 4
}

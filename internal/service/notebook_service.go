package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type NotebookService struct {
	repo        *repository.NotebookRepository
	logger      *zap.Logger
	driveAccess interface {
		ListAccessibleDriveFileIDs(context.Context, int64, []uuid.UUID) ([]uuid.UUID, error)
	}
}

func NewNotebookService(repo *repository.NotebookRepository, logger *zap.Logger, driveRepo ...*repository.DriveRepository) *NotebookService {
	s := &NotebookService{
		repo:   repo,
		logger: logger,
	}
	if len(driveRepo) > 0 {
		s.driveAccess = driveRepo[0]
	}
	return s
}

func (s *NotebookService) checkDocumentAccess(ctx context.Context, userID int64, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if s.driveAccess == nil {
		return apperrors.Internal("document access checker unavailable")
	}
	accessible, err := s.driveAccess.ListAccessibleDriveFileIDs(ctx, userID, ids)
	if err != nil {
		return err
	}
	allowed := make(map[uuid.UUID]bool, len(accessible))
	for _, id := range accessible {
		allowed[id] = true
	}
	for _, id := range ids {
		if !allowed[id] {
			return apperrors.Forbidden("You don't have access to this document")
		}
	}
	return nil
}

func (s *NotebookService) CreateChat(ctx context.Context, userID int64, req *domain.CreateNotebookChatRequest) (*domain.NotebookChat, error) {
	return s.repo.CreateChat(ctx, userID, req.Title)
}

func (s *NotebookService) GetUserChats(ctx context.Context, userID int64) ([]*domain.NotebookChat, error) {
	return s.repo.GetUserChats(ctx, userID)
}

func (s *NotebookService) GetChatHistory(ctx context.Context, userID int64, chatID uuid.UUID) ([]*domain.NotebookMessage, error) {
	// Verify chat ownership
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		s.logger.Warn("Chat not found or DB error", zap.String("chat_id", chatID.String()), zap.Error(err))
		return nil, apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return nil, apperrors.Forbidden("You don't have access to this chat")
	}

	messages, err := s.repo.GetChatMessages(ctx, chatID)
	if err != nil {
		s.logger.Error("Failed to fetch messages from DB", zap.Error(err))
		return nil, err
	}
	return messages, nil
}

func (s *NotebookService) SyncMessages(ctx context.Context, userID int64, chatID uuid.UUID, req *domain.SaveNotebookMessageRequest) error {
	// 1. Verify ownership
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return apperrors.Forbidden("Access denied")
	}

	// 2. Add user message (always text type)
	if err := s.repo.AddMessageWithType(ctx, chatID, "user", domain.NotebookMessageTypeText, req.UserMessage); err != nil {
		s.logger.Error("Failed to save user message", zap.Error(err))
		return apperrors.InternalWithErr("failed to save user message", err)
	}

	// 3. Add AI message with its specific type
	assistantType := req.AssistantType
	if assistantType == "" {
		assistantType = domain.NotebookMessageTypeText
	}

	if err := s.repo.AddMessageWithType(ctx, chatID, "assistant", assistantType, req.AssistantMessage); err != nil {
		s.logger.Error("Failed to save ai message", zap.Error(err))
		return apperrors.InternalWithErr("failed to save ai message", err)
	}

	return nil
}

func (s *NotebookService) DeleteChat(ctx context.Context, userID int64, chatID uuid.UUID) error {
	return s.repo.DeleteChat(ctx, chatID, userID)
}

func (s *NotebookService) SaveChatDocuments(ctx context.Context, userID int64, chatID uuid.UUID, docIDs []uuid.UUID) error {
	// Verify ownership
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return apperrors.Forbidden("Access denied")
	}

	if err := s.checkDocumentAccess(ctx, userID, docIDs); err != nil {
		return err
	}
	return s.repo.SaveChatDocuments(ctx, chatID, docIDs)
}

func (s *NotebookService) GetChatDocuments(ctx context.Context, userID int64, chatID uuid.UUID) ([]*domain.DriveFile, error) {
	// Verify ownership
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return nil, apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return nil, apperrors.Forbidden("Access denied")
	}

	docs, err := s.repo.GetChatDocuments(ctx, chatID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	if len(ids) == 0 {
		return docs, nil
	}
	if s.driveAccess == nil {
		return nil, apperrors.Internal("document access checker unavailable")
	}
	accessible, err := s.driveAccess.ListAccessibleDriveFileIDs(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	allowed := map[uuid.UUID]bool{}
	for _, id := range accessible {
		allowed[id] = true
	}
	visible := make([]*domain.DriveFile, 0, len(docs))
	for _, doc := range docs {
		if allowed[doc.ID] {
			visible = append(visible, doc)
		}
	}
	return visible, nil
}

func (s *NotebookService) GetChat(ctx context.Context, userID int64, chatID uuid.UUID) (*domain.NotebookChat, error) {
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return nil, apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return nil, apperrors.Forbidden("Access denied")
	}
	return chat, nil
}

func (s *NotebookService) UpdateChat(ctx context.Context, userID int64, chatID uuid.UUID, req *domain.UpdateNotebookChatRequest) error {
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return apperrors.Forbidden("Access denied")
	}
	return s.repo.UpdateChatTitle(ctx, chatID, req.Title)
}

func (s *NotebookService) AddDocument(ctx context.Context, userID int64, chatID, docID uuid.UUID) error {
	// Verify chat
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return apperrors.Forbidden("Access denied")
	}
	if err := s.checkDocumentAccess(ctx, userID, []uuid.UUID{docID}); err != nil {
		return err
	}
	return s.repo.AddChatDocument(ctx, chatID, docID)
}

func (s *NotebookService) RemoveDocument(ctx context.Context, userID int64, chatID, docID uuid.UUID) error {
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return apperrors.NotFound("Chat not found")
	}
	if chat.UserID != userID {
		return apperrors.Forbidden("Access denied")
	}
	return s.repo.RemoveChatDocument(ctx, chatID, docID)
}

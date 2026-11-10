package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type WhiteboardService struct {
	boardRepo      *repository.BoardRepository
	whiteboardRepo *repository.WhiteboardRepository
	logger         *zap.Logger
}

func NewWhiteboardService(boardRepo *repository.BoardRepository, whiteboardRepo *repository.WhiteboardRepository, logger *zap.Logger) *WhiteboardService {
	return &WhiteboardService{
		boardRepo:      boardRepo,
		whiteboardRepo: whiteboardRepo,
		logger:         logger.With(zap.String("service", "whiteboard")),
	}
}

func (s *WhiteboardService) CanConnect(ctx context.Context, userID int64, boardID uuid.UUID) error {
	if _, err := s.boardRepo.GetBoard(ctx, boardID); err != nil {
		return apperrors.NotFound("Доска")
	}

	ok, err := s.boardRepo.HasKanbanAccess(ctx, boardID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to check whiteboard access", err)
	}
	if !ok {
		return apperrors.Forbidden("У вас нет доступа к этой доске")
	}

	return nil
}

func (s *WhiteboardService) CanMutate(ctx context.Context, userID int64, boardID uuid.UUID, messageType string) error {
	role, err := s.getEffectiveRole(ctx, boardID, userID)
	if err != nil {
		return err
	}

	switch messageType {
	case domain.WhiteboardMessageTypeDraw, domain.WhiteboardMessageTypeUndo, domain.WhiteboardMessageTypeClear:
		if role == domain.BoardRoleMember {
			return apperrors.Forbidden("Для редактирования доски требуется роль editor или owner")
		}
	case domain.WhiteboardMessageTypeCursor, domain.WhiteboardMessageTypePing:
		return nil
	default:
		return apperrors.BadRequest(fmt.Sprintf("unsupported whiteboard message type: %s", messageType))
	}

	return nil
}

func (s *WhiteboardService) GetState(ctx context.Context, roomID uuid.UUID) ([]domain.WSMessage, error) {
	return s.whiteboardRepo.GetState(ctx, roomID)
}

func (s *WhiteboardService) SaveState(ctx context.Context, roomID uuid.UUID, state []domain.WSMessage, userID int64) error {
	return s.whiteboardRepo.UpsertState(ctx, roomID, state, userID)
}

func (s *WhiteboardService) DeleteState(ctx context.Context, roomID uuid.UUID) error {
	return s.whiteboardRepo.DeleteState(ctx, roomID)
}

func (s *WhiteboardService) getEffectiveRole(ctx context.Context, boardID uuid.UUID, userID int64) (string, error) {
	role, err := s.boardRepo.GetBoardMemberRole(ctx, boardID, userID)
	if err == nil && role != "" {
		return role, nil
	}

	ok, accessErr := s.boardRepo.HasKanbanAccess(ctx, boardID, userID)
	if accessErr != nil {
		return "", apperrors.InternalWithErr("failed to resolve whiteboard access", accessErr)
	}
	if !ok {
		return "", apperrors.Forbidden("У вас нет доступа к этой доске")
	}

	return domain.BoardRoleMember, nil
}

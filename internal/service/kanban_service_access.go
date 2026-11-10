package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func (s *KanbanService) checkKanbanAccess(ctx context.Context, boardID uuid.UUID, userID int64) error {
	hasAccess, err := s.boardRepo.HasKanbanAccess(ctx, boardID, userID)
	if err != nil {
		return apperrors.Internal("Не удалось проверить права доступа к доске")
	}
	if !hasAccess {
		return apperrors.Forbidden("У вас нет доступа к этой доске")
	}
	return nil
}

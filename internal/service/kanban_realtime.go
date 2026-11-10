package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type kanbanRealtimeBoards interface {
	HasKanbanAccess(context.Context, uuid.UUID, int64) (bool, error)
	GetBoard(context.Context, uuid.UUID) (*domain.Board, error)
	GetColumn(context.Context, uuid.UUID) (*domain.KanbanColumn, error)
	GetMembers(context.Context, uuid.UUID) ([]*domain.BoardMember, error)
}

type kanbanRealtimeTasks interface {
	GetByID(context.Context, uuid.UUID) (*domain.Task, error)
}

// ResolveTaskMovedRecipients validates a notification after the REST move has
// succeeded. It does not repeat the mutation or its history/gamification effects.
func (s *KanbanService) ResolveTaskMovedRecipients(ctx context.Context, userID int64, boardID, taskID, fromColumnID, toColumnID uuid.UUID) ([]int64, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}
	return resolveTaskMovedRecipients(ctx, s.boardRepo, s.taskRepo, userID, boardID, taskID, fromColumnID, toColumnID)
}

func resolveTaskMovedRecipients(ctx context.Context, boards kanbanRealtimeBoards, tasks kanbanRealtimeTasks, userID int64, boardID, taskID, fromColumnID, toColumnID uuid.UUID) ([]int64, error) {
	allowed, err := boards.HasKanbanAccess(ctx, boardID, userID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, apperrors.Forbidden("У вас нет доступа к этой доске")
	}
	task, err := tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.BoardID != boardID || task.ColumnID != toColumnID {
		return nil, apperrors.BadRequest("Событие не соответствует текущему положению задачи")
	}
	for _, id := range []uuid.UUID{fromColumnID, toColumnID} {
		column, err := boards.GetColumn(ctx, id)
		if err != nil {
			return nil, err
		}
		if column == nil || column.BoardID != boardID {
			return nil, apperrors.BadRequest("Колонка не принадлежит доске")
		}
	}
	return kanbanBoardRecipients(ctx, boards, boardID)
}

type kanbanRecipientStore interface {
	GetBoard(context.Context, uuid.UUID) (*domain.Board, error)
	GetMembers(context.Context, uuid.UUID) ([]*domain.BoardMember, error)
}

func kanbanBoardRecipients(ctx context.Context, boards kanbanRecipientStore, boardID uuid.UUID) ([]int64, error) {
	board, err := boards.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if board == nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}
	members, err := boards.GetMembers(ctx, boardID)
	if err != nil {
		return nil, err
	}
	recipients := []int64{board.OwnerID}
	seen := map[int64]bool{board.OwnerID: true}
	for _, member := range members {
		if member != nil && !seen[member.UserID] {
			seen[member.UserID] = true
			recipients = append(recipients, member.UserID)
		}
	}
	return recipients, nil
}

type kanbanRealtimeSender interface {
	SendToUser(int64, *websocket.Message) int
}

type kanbanRealtimeNotifier struct {
	boards kanbanRecipientStore
	sender kanbanRealtimeSender
	logger *zap.Logger
}

// SetRealtimeHub must be called at startup, before serving requests.
func (s *KanbanService) SetRealtimeHub(hub *websocket.Hub) {
	if hub != nil {
		s.realtime = &kanbanRealtimeNotifier{boards: s.boardRepo, sender: hub, logger: s.logger}
	}
}

func (n *kanbanRealtimeNotifier) notify(ctx context.Context, event *websocket.Message) {
	if n == nil {
		return
	}
	// The write is already committed. A disconnected HTTP client must not cancel
	// delivery to other board members, nor should delivery failure undo the write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	recipients, err := kanbanBoardRecipients(ctx, n.boards, *event.BoardID)
	if err != nil {
		n.logger.Warn("Failed to resolve kanban event recipients", zap.Error(err), zap.String("type", event.Type))
		return
	}
	event.Timestamp = time.Now().Unix()
	for _, userID := range recipients {
		if userID > 0 {
			n.sender.SendToUser(userID, event)
		}
	}
}

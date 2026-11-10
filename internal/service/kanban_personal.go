package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func (s *KanbanService) ListMyTasks(ctx context.Context, userID int64, f domain.MyTasksFilter) (*domain.MyTasksResponse, error) {
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 {
		return nil, apperrors.BadRequest("limit должен быть от 1 до 100, offset неотрицательным")
	}
	if f.Priority != "" {
		if err := validateTaskPriority(&f.Priority); err != nil {
			return nil, err
		}
	}
	f.Status = strings.TrimSpace(f.Status)
	tasks, err := s.taskRepo.ListMyTasks(ctx, userID, f)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось загрузить мои задачи", err)
	}
	more := len(tasks) > f.Limit
	if more {
		tasks = tasks[:f.Limit]
	}
	if tasks == nil {
		tasks = []*domain.Task{}
	}
	return &domain.MyTasksResponse{Tasks: tasks, Limit: f.Limit, Offset: f.Offset, HasMore: more}, nil
}

type deadlineReminderStore interface {
	ListTaskDeadlineReminders(context.Context, time.Time) ([]domain.TaskDeadlineReminder, error)
	ClaimTaskDeadlineReminder(context.Context, domain.TaskDeadlineReminder, uuid.UUID) (bool, error)
	FinishTaskDeadlineReminder(context.Context, domain.TaskDeadlineReminder, uuid.UUID, bool) error
}
type deadlineNotificationSender interface {
	CreateAndSendNotification(context.Context, domain.CreateNotificationRequest) error
}

func (s *KanbanService) StartDeadlineWorker(ctx context.Context) {
	if s.notifService == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		runCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
		processTaskDeadlines(runCtx, s.taskRepo, s.notifService, s.logger)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func processTaskDeadlines(ctx context.Context, store deadlineReminderStore, sender deadlineNotificationSender, logger *zap.Logger) {
	items, err := store.ListTaskDeadlineReminders(ctx, time.Now())
	if err != nil {
		logger.Error("Failed to list task deadlines", zap.Error(err))
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		token := uuid.New()
		claimed, err := store.ClaimTaskDeadlineReminder(ctx, item, token)
		if err != nil {
			logger.Warn("Failed to claim task deadline", zap.Error(err))
			continue
		}
		if !claimed {
			continue
		}
		err = sender.CreateAndSendNotification(ctx, taskDeadlineNotification(item))
		if err != nil {
			logger.Warn("Failed to send task deadline", zap.Error(err))
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		finishErr := store.FinishTaskDeadlineReminder(finishCtx, item, token, err == nil)
		cancel()
		if finishErr != nil {
			logger.Warn("Failed to finish task deadline", zap.Error(finishErr))
		}
	}
}

func taskDeadlineNotification(item domain.TaskDeadlineReminder) domain.CreateNotificationRequest {
	title := "Скоро срок задачи"
	if item.Kind == "overdue" {
		title = "Задача просрочена"
	}
	return domain.CreateNotificationRequest{
		TitleTemplate: &domain.NotificationTemplate{Source: title},
		UserID:        item.UserID, Type: "kanban_task_" + item.Kind, Title: title,
		ContentTemplate: &domain.NotificationTemplate{Source: "%s: %s. Срок: %s", Args: []any{item.TaskKey, item.Title, item.DueAt.Format(time.RFC3339)}},
		Content:         fmt.Sprintf("%s: %s. Срок: %s", item.TaskKey, item.Title, item.DueAt.Format(time.RFC3339)),
		Metadata:        map[string]string{"kind": "kanban_task", "action": item.Kind, "task_id": item.TaskID.String(), "board_id": item.BoardID.String(), "task_key": item.TaskKey, "due_at": item.DueAt.Format(time.RFC3339)},
	}
}

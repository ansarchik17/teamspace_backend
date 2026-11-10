package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/websocket"
)

func validateChecklistFields(title *string, position *int) error {
	if title != nil {
		*title = strings.TrimSpace(*title)
		if *title == "" || utf8.RuneCountInString(*title) > 500 {
			return apperrors.BadRequest("Название пункта должно содержать от 1 до 500 символов")
		}
	}
	if position != nil && (*position < 0 || *position > 1000000) {
		return apperrors.BadRequest("position должен быть от 0 до 1000000")
	}
	return nil
}

func (s *KanbanService) contentTask(ctx context.Context, userID int64, taskID uuid.UUID) (*domain.Task, error) {
	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccess(ctx, task.BoardID, userID); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *KanbanService) editableContentTask(ctx context.Context, userID int64, taskID uuid.UUID) (*domain.Task, error) {
	task, err := s.contentTask(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	if err = s.checkAccessEditTask(ctx, task.BoardID, userID); err != nil {
		return nil, err
	}
	return task, nil
}

// Publish after a successful write without making the HTTP response wait for
// recipient lookup. Clients reconcile through REST after reconnecting.
func (s *KanbanService) notifyTaskContent(task *domain.Task, userID int64, eventType string, content any) {
	if s.realtime == nil {
		return
	}
	boardID, taskID := task.BoardID, task.ID
	event := &websocket.Message{Type: eventType, From: userID, BoardID: &boardID, TaskID: &taskID, Content: content}
	go s.realtime.notify(context.Background(), event)
}

func (s *KanbanService) notifySubtaskParents(task *domain.Task, userID int64, previousParent *uuid.UUID) {
	seen := map[uuid.UUID]bool{}
	for _, parentID := range []*uuid.UUID{previousParent, task.ParentID} {
		if parentID == nil || seen[*parentID] {
			continue
		}
		seen[*parentID] = true
		parent := &domain.Task{ID: *parentID, BoardID: task.BoardID}
		s.notifyTaskContent(parent, userID, "kanban_subtasks_changed", map[string]any{"subtask_id": task.ID})
	}
}

func (s *KanbanService) ListChecklist(ctx context.Context, userID int64, taskID uuid.UUID) ([]*domain.TaskChecklistItem, error) {
	if _, err := s.contentTask(ctx, userID, taskID); err != nil {
		return nil, err
	}
	return s.taskRepo.ListChecklist(ctx, taskID)
}

func (s *KanbanService) CreateChecklistItem(ctx context.Context, userID int64, taskID uuid.UUID, req domain.CreateTaskChecklistItem) (*domain.TaskChecklistItem, error) {
	if err := validateChecklistFields(&req.Title, req.Position); err != nil {
		return nil, err
	}
	task, err := s.editableContentTask(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	item, err := s.taskRepo.CreateChecklistItem(ctx, taskID, userID, req)
	if err != nil {
		return nil, checklistError(err)
	}
	s.notifyTaskContent(task, userID, "kanban_checklist_changed", map[string]any{"action": "created", "item": item})
	return item, nil
}

func (s *KanbanService) UpdateChecklistItem(ctx context.Context, userID int64, taskID, itemID uuid.UUID, req domain.UpdateTaskChecklistItem) (*domain.TaskChecklistItem, error) {
	if req.Title == nil && req.IsCompleted == nil && req.Position == nil {
		return nil, apperrors.BadRequest("Не указаны поля пункта")
	}
	if err := validateChecklistFields(req.Title, req.Position); err != nil {
		return nil, err
	}
	task, err := s.editableContentTask(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	item, err := s.taskRepo.UpdateChecklistItem(ctx, taskID, itemID, userID, req)
	if err != nil {
		return nil, checklistError(err)
	}
	s.notifyTaskContent(task, userID, "kanban_checklist_changed", map[string]any{"action": "updated", "item": item})
	return item, nil
}

func (s *KanbanService) DeleteChecklistItem(ctx context.Context, userID int64, taskID, itemID uuid.UUID) error {
	task, err := s.editableContentTask(ctx, userID, taskID)
	if err != nil {
		return err
	}
	if err := s.taskRepo.DeleteChecklistItem(ctx, taskID, itemID, userID); err != nil {
		return checklistError(err)
	}
	s.notifyTaskContent(task, userID, "kanban_checklist_changed", map[string]any{"action": "deleted", "item_id": itemID})
	return nil
}

func checklistError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NotFound("Пункт чеклиста не найден")
	}
	return apperrors.InternalWithErr("Не удалось сохранить чеклист", err)
}

func validateTaskComment(content string, attachment *string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" && (attachment == nil || strings.TrimSpace(*attachment) == "") {
		return "", apperrors.BadRequest("Укажите текст комментария или вложение")
	}
	if utf8.RuneCountInString(content) > 10000 {
		return "", apperrors.BadRequest("Комментарий не должен превышать 10000 символов")
	}
	return content, nil
}

func (s *KanbanService) validateAttachmentSources(ctx context.Context, userID int64, fileIDs []uuid.UUID) error {
	if len(fileIDs) == 0 {
		return nil
	}
	if s.driveRepo == nil {
		return apperrors.Internal("Хранилище файлов недоступно")
	}
	for _, fileID := range fileIDs {
		file, err := s.driveRepo.GetFileByID(ctx, fileID)
		if err != nil || file == nil {
			return apperrors.NotFound("Загруженный файл не найден")
		}
		if file.OwnerID != userID || file.Scope != "kanban" {
			return apperrors.Forbidden("Можно прикреплять только свои загруженные файлы Kanban")
		}
	}
	return nil
}

// Resolve the owning task, then use the same access checks as nested routes.
func (s *KanbanService) ChecklistTaskID(ctx context.Context, userID int64, itemID uuid.UUID) (uuid.UUID, error) {
	taskID, err := s.taskRepo.ChecklistTaskID(ctx, itemID)
	if err != nil {
		return uuid.Nil, checklistError(err)
	}
	if _, err = s.editableContentTask(ctx, userID, taskID); err != nil {
		return uuid.Nil, err
	}
	return taskID, nil
}

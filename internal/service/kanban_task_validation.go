package service

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func validateCreateTask(req *CreateTaskRequest) error {
	if req.BoardID == uuid.Nil {
		return apperrors.BadRequest("board_id обязателен")
	}
	if err := validateTaskTitle(&req.Title); err != nil {
		return err
	}
	if req.Priority == "" {
		req.Priority = "medium"
	}
	if err := validateTaskPriority(&req.Priority); err != nil {
		return err
	}
	if req.OriginalEstimateSec < 0 {
		return apperrors.BadRequest("original_estimate_sec не может быть отрицательным")
	}
	if err := validateTaskDueAt(req.DueAt); err != nil {
		return err
	}
	return validateTaskAssignees(req.AssigneeUserIDs, req.AssigneeUserID)
}

func validateTaskPatch(patch *domain.UpdateTaskPatch) error {
	if patch == nil {
		return apperrors.BadRequest("Не указаны поля задачи")
	}
	if patch.Title != nil {
		if err := validateTaskTitle(patch.Title); err != nil {
			return err
		}
	}
	if patch.Priority != nil {
		if err := validateTaskPriority(patch.Priority); err != nil {
			return err
		}
	}
	if patch.OriginalEstimateSec != nil && *patch.OriginalEstimateSec < 0 {
		return apperrors.BadRequest("original_estimate_sec не может быть отрицательным")
	}
	if patch.ClearDueAt && patch.DueAt != nil {
		return apperrors.BadRequest("Нельзя одновременно установить и удалить due_at")
	}
	if patch.ClearDescription && patch.Description != nil {
		return apperrors.BadRequest("Нельзя одновременно установить и удалить description")
	}
	if err := validateTaskDueAt(patch.DueAt); err != nil {
		return err
	}
	if err := validateTaskAssignees(patch.AssigneeIDs, patch.AssigneeID); err != nil {
		return err
	}
	// Preserve nil (no change) versus [] (remove all assignees).
	if patch.AssigneeIDs != nil || patch.AssigneeID != nil {
		patch.AssigneeIDs = normalizeAssigneeIDs(patch.AssigneeIDs, patch.AssigneeID)
		if patch.AssigneeIDs == nil {
			patch.AssigneeIDs = []int64{}
		}
	}
	return nil
}

func validateTaskTitle(title *string) error {
	*title = strings.TrimSpace(*title)
	if *title == "" {
		return apperrors.BadRequest("title обязателен и не может состоять из пробелов")
	}
	return nil
}

func validateTaskPriority(priority *string) error {
	*priority = strings.ToLower(strings.TrimSpace(*priority))
	switch *priority {
	case "low", "medium", "high", "critical":
		return nil
	default:
		return apperrors.BadRequest("priority должен быть low, medium, high или critical")
	}
}

func validateTaskDueAt(due *time.Time) error {
	// Past deadlines are valid for overdue tasks. Reject unusable zero/out-of-range dates.
	if due != nil && (due.IsZero() || due.Year() < 1 || due.Year() > 9999) {
		return apperrors.BadRequest("due_at должен содержать корректную дату RFC3339")
	}
	return nil
}

func validateTaskAssignees(ids []int64, legacyID *int64) error {
	for _, id := range ids {
		if id <= 0 {
			return apperrors.BadRequest("assignee_ids должны содержать положительные ID пользователей")
		}
	}
	if legacyID != nil && *legacyID <= 0 {
		return apperrors.BadRequest("assignee_id должен быть положительным")
	}
	return nil
}

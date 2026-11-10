package repository

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// mutateTaskContent commits a content change and its audit entry together.
// SQL returns task_id, old JSON text and new JSON text (nullable on insert/delete).
func (r *TaskRepository) mutateTaskContent(ctx context.Context, userID int64, action, field, query string, args ...any) (json.RawMessage, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var taskID uuid.UUID
	var before, after *string
	if err = tx.QueryRow(ctx, query, args...).Scan(&taskID, &before, &after); err != nil {
		return nil, err
	}
	if err = r.addHistoryInTx(ctx, tx, &domain.TaskHistory{TaskID: taskID, UserID: userID, Action: action, FieldName: &field, OldValue: before, NewValue: after}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if after == nil {
		return nil, nil
	}
	return json.RawMessage(*after), nil
}

// Includes soft-deleted tasks and retained history after physical task deletion.
func (r *TaskRepository) GetHistoryBoardID(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error) {
	var boardID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT board_id FROM tasks WHERE id=$1 UNION ALL SELECT board_id FROM task_history WHERE task_id=$1 AND board_id IS NOT NULL LIMIT 1`, taskID).Scan(&boardID)
	return boardID, err
}

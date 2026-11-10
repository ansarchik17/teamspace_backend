package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type odoTaskQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ODOTaskFingerprint(ctx context.Context, db odoTaskQuery, id uuid.UUID) (string, error) {
	var raw []byte
	err := db.QueryRow(ctx, `SELECT jsonb_build_object('task',to_jsonb(t),'assignees',COALESCE((SELECT jsonb_agg(user_id ORDER BY user_id) FROM task_assignees WHERE task_id=t.id),'[]'::jsonb)) FROM tasks t WHERE t.id=$1`, id).Scan(&raw)
	if err != nil {
		return "", err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return "", err
	}
	normalizeODOTimestamps(value)
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func normalizeODOTimestamps(value any) {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			switch k {
			case "created_at", "updated_at", "due_at", "deleted_at", "completed_at", "closed_at", "source_completed_at", "source_closed_at":
				if s, ok := x.(string); ok {
					if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
						v[k] = t.UTC().Format(time.RFC3339Nano)
					}
				}
			}
			normalizeODOTimestamps(x)
		}
	case []any:
		for _, x := range v {
			normalizeODOTimestamps(x)
		}
	}
}

func (r *TaskRepository) ApplyODOBackfill(ctx context.Context, actor int64, request domain.ODOAssignmentBackfill) (*domain.ODOAssignmentReceipt, error) {
	if request.OperationID == uuid.Nil || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 2000 || (request.TaskID != nil && (*request.TaskID == uuid.Nil || len(request.ExpectedTaskHash) != 64)) || (request.TaskID == nil && request.ExpectedTaskHash != "") {
		return nil, apperrors.Validation("reviewed backfill plan required")
	}
	return r.applyODOAssignment(ctx, actor, request.Event, "", &request)
}

func validateODOAdoption(ctx context.Context, tx pgx.Tx, t *domain.Task, org uuid.UUID) error {
	if t.SourceOrganizationID != nil || t.SourceRevision != 0 {
		return odoConflict("ODO_LEGACY_CONFLICT")
	}
	if t.GoalID != nil || t.ProjectID != nil || t.SprintID != nil || t.ParentID != nil {
		return apperrors.Validation("legacy task has unrelated Kanban context")
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_assignees WHERE task_id=$1)
 AND NOT EXISTS(SELECT 1 FROM task_assignees a LEFT JOIN users u ON u.id=a.user_id WHERE a.task_id=$1 AND u.organization_id IS DISTINCT FROM $2)
 AND NOT EXISTS(SELECT 1 FROM tasks WHERE parent_id=$1 AND deleted_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM task_attachments WHERE task_id=$1)
 AND NOT EXISTS(SELECT 1 FROM task_comments WHERE task_id=$1)
 AND NOT EXISTS(SELECT 1 FROM task_worklogs WHERE task_id=$1)
 AND NOT EXISTS(SELECT 1 FROM kanban_task_labels WHERE task_id=$1)
 AND NOT EXISTS(SELECT 1 FROM task_checklist_items WHERE task_id=$1)`, t.ID, org).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return apperrors.Forbidden("legacy recipients or attached context require manual reconciliation")
	}
	return nil
}

func moveODOProjection(ctx context.Context, tx pgx.Tx, t *domain.Task, board uuid.UUID) error {
	var prefix string
	var number int
	err := tx.QueryRow(ctx, `SELECT COALESCE(task_key_prefix,'ODO'),COALESCE(task_sequence,0)+1 FROM boards WHERE id=$1 FOR UPDATE`, board).Scan(&prefix, &number)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s-%d", prefix, number)
	if _, err = tx.Exec(ctx, `UPDATE tasks SET board_id=$2,task_number=$3,task_key=$4 WHERE id=$1`, t.ID, board, number, key); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE boards SET task_sequence=$2,updated_at=now() WHERE id=$1`, board, number); err != nil {
		return err
	}
	t.BoardID, t.TaskNumber, t.TaskKey = board, number, key
	return nil
}

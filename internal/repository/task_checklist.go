package repository

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const checklistFields = `id,task_id,title,is_completed,position,created_by,created_at,updated_at`

func scanChecklistItem(row pgx.Row) (*domain.TaskChecklistItem, error) {
	i := &domain.TaskChecklistItem{}
	err := row.Scan(&i.ID, &i.TaskID, &i.Title, &i.IsCompleted, &i.Position, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (r *TaskRepository) ListChecklist(ctx context.Context, taskID uuid.UUID) ([]*domain.TaskChecklistItem, error) {
	rows, err := r.db.Query(ctx, `SELECT `+checklistFields+` FROM task_checklist_items WHERE task_id=$1 ORDER BY position,created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*domain.TaskChecklistItem{}
	for rows.Next() {
		item, err := scanChecklistItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) CreateChecklistItem(ctx context.Context, taskID uuid.UUID, userID int64, req domain.CreateTaskChecklistItem) (*domain.TaskChecklistItem, error) {
	raw, err := r.mutateTaskContent(ctx, userID, "checklist_created", "checklist", `INSERT INTO task_checklist_items(id,task_id,title,position,created_by,is_completed)
 SELECT $1,t.id,$3,COALESCE($4::integer,(SELECT COALESCE(MAX(position)+1,0) FROM task_checklist_items WHERE task_id=t.id)),$5,$6
 FROM tasks t WHERE t.id=$2 AND t.deleted_at IS NULL RETURNING task_id,NULL::text,to_jsonb(task_checklist_items)::text`, uuid.New(), taskID, req.Title, req.Position, userID, req.IsCompleted)
	if err != nil {
		return nil, err
	}
	var item domain.TaskChecklistItem
	err = json.Unmarshal(raw, &item)
	return &item, err
}
func (r *TaskRepository) UpdateChecklistItem(ctx context.Context, taskID, itemID uuid.UUID, userID int64, req domain.UpdateTaskChecklistItem) (*domain.TaskChecklistItem, error) {
	raw, err := r.mutateTaskContent(ctx, userID, "checklist_updated", "checklist", `WITH old AS (SELECT * FROM task_checklist_items WHERE task_id=$1 AND id=$2 FOR UPDATE)
 UPDATE task_checklist_items i SET title=COALESCE($3,i.title),is_completed=COALESCE($4,i.is_completed),position=COALESCE($5,i.position),updated_at=NOW() FROM old
 WHERE i.id=old.id AND EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND deleted_at IS NULL)
 RETURNING i.task_id,to_jsonb(old)::text,to_jsonb(i)::text`, taskID, itemID, req.Title, req.IsCompleted, req.Position)
	if err != nil {
		return nil, err
	}
	var item domain.TaskChecklistItem
	err = json.Unmarshal(raw, &item)
	return &item, err
}
func (r *TaskRepository) DeleteChecklistItem(ctx context.Context, taskID, itemID uuid.UUID, userID int64) error {
	_, err := r.mutateTaskContent(ctx, userID, "checklist_deleted", "checklist", `DELETE FROM task_checklist_items WHERE task_id=$1 AND id=$2 RETURNING task_id,to_jsonb(task_checklist_items)::text,NULL::text`, taskID, itemID)
	return err
}

func (r *TaskRepository) ChecklistTaskID(ctx context.Context, itemID uuid.UUID) (uuid.UUID, error) {
	var taskID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT task_id FROM task_checklist_items WHERE id=$1`, itemID).Scan(&taskID)
	return taskID, err
}

package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// A search page needs four queries regardless of the number of returned tasks.
func (r *TaskRepository) scanSearchTasks(ctx context.Context, rows pgx.Rows) ([]*domain.Task, error) {
	tasks, err := scanTaskRowsBase(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return []*domain.Task{}, nil
	}
	ids := make([]uuid.UUID, 0, len(tasks))
	byID := make(map[uuid.UUID]*domain.Task, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
		byID[t.ID] = t
		t.Attachments = []*domain.TaskAttachment{}
	}
	return r.loadSearchDetails(ctx, tasks, ids, byID)
}

func (r *TaskRepository) loadSearchDetails(ctx context.Context, tasks []*domain.Task, ids []uuid.UUID, byID map[uuid.UUID]*domain.Task) ([]*domain.Task, error) {
	rows, err := r.db.Query(ctx, `SELECT task_id,user_id FROM task_assignees WHERE task_id=ANY($1) ORDER BY user_id`, ids)
	if err != nil {
		return nil, err
	}
	var taskID uuid.UUID
	var userID int64
	_, err = pgx.ForEachRow(rows, []any{&taskID, &userID}, func() error { byID[taskID].AssigneeIDs = append(byID[taskID].AssigneeIDs, userID); return nil })
	if err != nil {
		return nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT id,task_id,uploaded_by,created_at,file_name,object_name,file_size,mime_type FROM task_attachments WHERE task_id=ANY($1) ORDER BY created_at DESC,id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &domain.TaskAttachment{}
		if err = rows.Scan(&a.ID, &a.TaskID, &a.UploadedBy, &a.CreatedAt, &a.FileName, &a.ObjectName, &a.FileSize, &a.MimeType); err != nil {
			rows.Close()
			return nil, err
		}
		a.DownloadURL = fmt.Sprintf("/api/v1/kanban/tasks/%s/files/%s/download", a.TaskID, a.ID)
		byID[a.TaskID].Attachments = append(byID[a.TaskID].Attachments, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT tl.task_id,l.id,l.board_id,l.name,l.color,l.created_at FROM kanban_labels l JOIN kanban_task_labels tl ON l.id=tl.label_id WHERE tl.task_id=ANY($1) ORDER BY l.name,l.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		l := &domain.KanbanLabel{}
		if err = rows.Scan(&taskID, &l.ID, &l.BoardID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
			return nil, err
		}
		byID[taskID].Labels = append(byID[taskID].Labels, l)
	}
	return tasks, rows.Err()
}

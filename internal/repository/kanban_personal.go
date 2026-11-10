package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const unfinishedTaskCondition = `UPPER(COALESCE(c.key,'')) <> 'DONE' AND UPPER(t.status) NOT IN ('ГОТОВО','DONE','COMPLETED','CLOSED','CANCELLED','CANCELED')`

func (r *TaskRepository) ListMyTasks(ctx context.Context, userID int64, f domain.MyTasksFilter) ([]*domain.Task, error) {
	rows, err := r.db.Query(ctx, `SELECT `+taskSelectColumnsWithAlias+`
	 FROM tasks t JOIN boards b ON b.id=t.board_id JOIN kanban_columns c ON c.id=t.column_id
	 WHERE t.deleted_at IS NULL AND `+kanbanBoardVisibilityCondition("b", "$1")+`
	 AND EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id=t.id AND ta.user_id=$1)
	 AND ($2::uuid IS NULL OR t.board_id=$2) AND ($3::uuid IS NULL OR t.project_id=$3)
	 AND ($4='' OR t.status=$4 OR c.key=$4) AND ($5='' OR t.priority=$5)
	 AND (NOT $6 OR (t.due_at < NOW() AND `+unfinishedTaskCondition+`))
	 ORDER BY t.due_at ASC NULLS LAST, t.created_at DESC, t.id
	 LIMIT $7 OFFSET $8`, userID, f.BoardID, f.ProjectID, f.Status, f.Priority, f.Overdue, f.Limit+1, f.Offset)
	if err != nil {
		return nil, err
	}
	tasks, err := scanTaskRowsBase(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		task.AssigneeIDs, err = r.GetAssignees(ctx, task.ID)
		if err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

func (r *TaskRepository) ListTaskDeadlineReminders(ctx context.Context, now time.Time) ([]domain.TaskDeadlineReminder, error) {
	rows, err := r.db.Query(ctx, `SELECT t.id,t.board_id,ta.user_id,t.title,t.task_key,t.due_at,
	 CASE WHEN t.due_at <= $1 THEN 'overdue' ELSE 'due_soon' END
	 FROM tasks t JOIN boards b ON b.id=t.board_id JOIN kanban_columns c ON c.id=t.column_id
	 JOIN task_assignees ta ON ta.task_id=t.id
	 WHERE t.deleted_at IS NULL AND `+kanbanBoardVisibilityCondition("b", "ta.user_id")+`
	 AND `+unfinishedTaskCondition+` AND t.due_at <= $1::timestamptz + INTERVAL '24 hours'
	 AND NOT EXISTS (SELECT 1 FROM kanban_deadline_deliveries d WHERE d.task_id=t.id AND d.user_id=ta.user_id
	 AND d.due_at=t.due_at AND d.kind=CASE WHEN t.due_at <= $1 THEN 'overdue' ELSE 'due_soon' END
	 AND (d.sent_at IS NOT NULL OR d.lease_until > $1))
	 ORDER BY t.due_at,t.id,ta.user_id LIMIT 200`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.TaskDeadlineReminder{}
	for rows.Next() {
		var item domain.TaskDeadlineReminder
		if err := rows.Scan(&item.TaskID, &item.BoardID, &item.UserID, &item.Title, &item.TaskKey, &item.DueAt, &item.Kind); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) ClaimTaskDeadlineReminder(ctx context.Context, item domain.TaskDeadlineReminder, token uuid.UUID) (bool, error) {
	var claimed uuid.UUID
	err := r.db.QueryRow(ctx, `INSERT INTO kanban_deadline_deliveries(task_id,user_id,due_at,kind,token,lease_until)
	 SELECT t.id,$2,t.due_at,$4,$5,NOW()+INTERVAL '2 minutes'
	 FROM tasks t JOIN boards b ON b.id=t.board_id JOIN kanban_columns c ON c.id=t.column_id
	 WHERE t.id=$1 AND t.due_at=$3 AND t.deleted_at IS NULL AND `+unfinishedTaskCondition+`
	 AND `+kanbanBoardVisibilityCondition("b", "$2")+`
	 AND EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id=t.id AND ta.user_id=$2)
	 AND (($4='overdue' AND t.due_at<=NOW()) OR ($4='due_soon' AND t.due_at>NOW() AND t.due_at<=NOW()+INTERVAL '24 hours'))
	 ON CONFLICT(task_id,user_id,due_at,kind) DO UPDATE SET token=EXCLUDED.token,lease_until=EXCLUDED.lease_until
	 WHERE kanban_deadline_deliveries.sent_at IS NULL AND kanban_deadline_deliveries.lease_until<=NOW()
	 RETURNING token`, item.TaskID, item.UserID, item.DueAt, item.Kind, token).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *TaskRepository) FinishTaskDeadlineReminder(ctx context.Context, item domain.TaskDeadlineReminder, token uuid.UUID, success bool) error {
	_, err := r.db.Exec(ctx, `UPDATE kanban_deadline_deliveries SET
	 sent_at=CASE WHEN $6 THEN NOW() ELSE NULL END, lease_until=NOW()+INTERVAL '1 minute'
	 WHERE task_id=$1 AND user_id=$2 AND due_at=$3 AND kind=$4 AND token=$5`, item.TaskID, item.UserID, item.DueAt, item.Kind, token, success)
	return err
}

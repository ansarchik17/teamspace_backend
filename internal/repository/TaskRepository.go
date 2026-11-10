package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type TaskRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewTaskRepository(db *pgxpool.Pool, logger *zap.Logger) *TaskRepository {
	return &TaskRepository{db: db, logger: logger.With(zap.String("repository", "task"))}
}

const taskSelectColumns = `
	id, board_id, goal_id, project_id, sprint_id, column_id, created_by, task_number, task_key,
	title, description, priority, due_at, original_estimate_sec, time_spent_sec,
	created_at, updated_at, parent_id, status
`

const taskSelectColumnsWithAlias = `
	t.id, t.board_id, t.goal_id, t.project_id, t.sprint_id, t.column_id, t.created_by, t.task_number, t.task_key,
	t.title, t.description, t.priority, t.due_at, t.original_estimate_sec, t.time_spent_sec,
	t.created_at, t.updated_at, t.parent_id, t.status
`

func (r *TaskRepository) Create(ctx context.Context, t *domain.Task) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now

	var prefix string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(task_key_prefix, 'AB'), COALESCE(task_sequence, 0) + 1
		FROM boards
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, t.BoardID).Scan(&prefix, &t.TaskNumber); err != nil {
		return fmt.Errorf("allocate task number: %w", err)
	}
	t.TaskKey = fmt.Sprintf("%s-%d", prefix, t.TaskNumber)
	setTaskLink(t)

	assigneeID := t.CreatedBy
	if len(t.AssigneeIDs) > 0 {
		assigneeID = t.AssigneeIDs[0]
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO tasks (
            id, board_id, goal_id, project_id, sprint_id, column_id, created_by, task_number, task_key,
            title, description, priority, due_at, original_estimate_sec, 
            created_at, updated_at, parent_id, status, assignee_id
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
    `,
		t.ID, t.BoardID, t.GoalID, t.ProjectID, t.SprintID, t.ColumnID, t.CreatedBy, t.TaskNumber, t.TaskKey,
		t.Title, t.Description, t.Priority, t.DueAt, t.OriginalEstimateSec,
		t.CreatedAt, t.UpdatedAt, t.ParentID, t.Status, assigneeID)

	if err != nil {
		return fmt.Errorf("db insert error: %w", err)
	}

	// Keep ordinary task creation independent from optional integration columns.
	// This also makes rolling deployments safe while the external-source migration
	// is being applied to all database instances.
	if strings.TrimSpace(t.SourceSystem) != "" ||
		strings.TrimSpace(t.SourceAssignmentID) != "" ||
		strings.TrimSpace(t.SourceDocumentID) != "" ||
		strings.TrimSpace(t.SourceURL) != "" ||
		strings.TrimSpace(t.SourceAuthorSub) != "" ||
		strings.TrimSpace(t.SourceDepartmentID) != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET source_system = $2,
				source_assignment_id = $3,
				source_document_id = $4,
				source_url = $5,
				source_author_sub = $6,
				source_department_id = $7
			WHERE id = $1
		`, t.ID, t.SourceSystem, t.SourceAssignmentID, t.SourceDocumentID, t.SourceURL, t.SourceAuthorSub, t.SourceDepartmentID); err != nil {
			return fmt.Errorf("update task external source: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE boards SET task_sequence = $2, updated_at = NOW() WHERE id = $1`, t.BoardID, t.TaskNumber); err != nil {
		return fmt.Errorf("update board task sequence: %w", err)
	}

	if len(t.AssigneeIDs) > 0 {
		for _, uid := range uniqueInt64(t.AssigneeIDs) {
			_, err := tx.Exec(ctx, `
                INSERT INTO task_assignees (task_id, user_id) 
                VALUES ($1, $2) 
                ON CONFLICT DO NOTHING
            `, t.ID, uid)
			if err != nil {
				return fmt.Errorf("assignee insert error for user %d: %w", uid, err)
			}
		}
	}

	snapshot, err := json.Marshal(t)
	if err != nil {
		return err
	}
	// Creation and its initial state are committed together.
	history := &domain.TaskHistory{
		TaskID:    t.ID,
		UserID:    t.CreatedBy,
		Action:    domain.ActionTaskCreated,
		NewValue:  strPtr(string(snapshot)),
		CreatedAt: now,
	}
	if err := r.addHistoryInTx(ctx, tx, history); err != nil {
		return fmt.Errorf("record task creation: %w", err)
	}

	// Логируем назначение исполнителей
	if len(t.AssigneeIDs) > 0 {
		for _, assigneeID := range uniqueInt64(t.AssigneeIDs) {
			assigneeHistory := &domain.TaskHistory{
				TaskID:    t.ID,
				UserID:    t.CreatedBy,
				Action:    domain.ActionTaskAssigned,
				NewValue:  strPtr(fmt.Sprintf("%d", assigneeID)),
				CreatedAt: now,
			}
			if err := r.addHistoryInTx(ctx, tx, assigneeHistory); err != nil {
				return fmt.Errorf("record task assignment: %w", err)
			}
		}
	}

	return tx.Commit(ctx)
}

func (r *TaskRepository) GetByDocumentologAssignment(ctx context.Context, assignmentID string) (*domain.Task, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM tasks WHERE source_system='documentolog' AND source_assignment_id=$1 AND deleted_at IS NULL`, assignmentID).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

// Вспомогательная функция для добавления истории внутри транзакции
func (r *TaskRepository) addHistoryInTx(ctx context.Context, tx pgx.Tx, history *domain.TaskHistory) error {
	if history.CreatedAt.IsZero() {
		history.CreatedAt = time.Now()
	}
	if history.ID == uuid.Nil {
		history.ID = uuid.New()
	}

	var metadataJSON interface{}
	if history.Metadata != nil {
		metadataJSON = history.Metadata
	}

	query := `
		INSERT INTO task_history 
		(id, task_id, user_id, action, field_name, old_value, new_value, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()))
	`

	_, err := tx.Exec(ctx, query,
		history.ID,
		history.TaskID,
		history.UserID,
		history.Action,
		history.FieldName,
		history.OldValue,
		history.NewValue,
		metadataJSON,
		history.CreatedAt,
	)

	return err
}

// Вспомогательная функция для создания указателя на строку
func strPtr(s string) *string {
	return &s
}

func uniqueInt64(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func (r *TaskRepository) ListByBoard(ctx context.Context, boardID uuid.UUID, sprintID *uuid.UUID) ([]*domain.Task, error) {
	q := `
        SELECT ` + taskSelectColumns + `
        FROM tasks
        WHERE board_id = $1 AND deleted_at IS NULL
    `
	args := []any{boardID}
	if sprintID != nil {
		q += ` AND sprint_id = $2`
		args = append(args, *sprintID)
	}

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTasks(ctx, rows)
}

func (r *TaskRepository) ListByBoardTopTasks(ctx context.Context, boardID uuid.UUID, limitPerColumn int) ([]*domain.Task, error) {
	q := `
        WITH RankedTasks AS (
            SELECT ` + taskSelectColumns + `,
                   ROW_NUMBER() OVER(PARTITION BY column_id ORDER BY created_at DESC) as rn
            FROM tasks
            WHERE board_id = $1 AND deleted_at IS NULL
        )
        SELECT ` + taskSelectColumns + `
        FROM RankedTasks
        WHERE rn <= $2
    `
	rows, err := r.db.Query(ctx, q, boardID, limitPerColumn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTasks(ctx, rows)
}

func (r *TaskRepository) GetColumnTaskCounts(ctx context.Context, boardID uuid.UUID) (map[uuid.UUID]int, error) {
	q := `
		SELECT column_id, COUNT(*)::int
		FROM tasks
		WHERE board_id = $1 AND deleted_at IS NULL AND parent_id IS NULL
		GROUP BY column_id
	`
	rows, err := r.db.Query(ctx, q, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[uuid.UUID]int)
	for rows.Next() {
		var colID uuid.UUID
		var count int
		if err := rows.Scan(&colID, &count); err != nil {
			return nil, err
		}
		counts[colID] = count
	}
	return counts, nil
}

func (r *TaskRepository) GetAssignees(ctx context.Context, taskID uuid.UUID) ([]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id FROM task_assignees WHERE task_id=$1 ORDER BY user_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (r *TaskRepository) ListBoardTasksByUser(ctx context.Context, boardID uuid.UUID, dateFrom, dateTo *time.Time) ([]domain.BoardTasksByUserItem, error) {
	query := `
		SELECT
			u.id,
			COALESCE(NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.username) AS fio,
			COALESCE(bm.role, '') AS role,
			bm.team_role,
			t.id,
			t.title,
			t.task_number,
			t.description,
			t.due_at,
			t.status
		FROM tasks t
		JOIN task_assignees ta ON ta.task_id = t.id
		JOIN users u ON u.id = ta.user_id
		LEFT JOIN board_members bm ON bm.board_id = t.board_id AND bm.user_id = u.id
		WHERE t.board_id = $1
		  AND t.deleted_at IS NULL
	`
	args := []any{boardID}
	if dateFrom != nil {
		query += ` AND t.due_at >= $2`
		args = append(args, *dateFrom)
	}
	if dateTo != nil {
		query += fmt.Sprintf(` AND t.due_at <= $%d`, len(args)+1)
		args = append(args, *dateTo)
	}
	query += ` ORDER BY fio ASC, t.due_at ASC NULLS LAST, t.task_number ASC`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grouped := make([]domain.BoardTasksByUserItem, 0)
	indexByUserID := make(map[int64]int)

	for rows.Next() {
		var (
			userID      int64
			fio         string
			role        string
			teamRole    *string
			taskID      uuid.UUID
			title       string
			number      int
			description *string
			endDate     *time.Time
			status      string
		)
		if err := rows.Scan(&userID, &fio, &role, &teamRole, &taskID, &title, &number, &description, &endDate, &status); err != nil {
			return nil, err
		}

		idx, exists := indexByUserID[userID]
		if !exists {
			grouped = append(grouped, domain.BoardTasksByUserItem{
				User: domain.BoardTasksByUserUser{
					ID:       userID,
					FIO:      fio,
					Role:     role,
					TeamRole: teamRole,
				},
				Tasks: make([]domain.BoardTasksByUserTask, 0),
			})
			idx = len(grouped) - 1
			indexByUserID[userID] = idx
		}

		grouped[idx].Tasks = append(grouped[idx].Tasks, domain.BoardTasksByUserTask{
			ID:          taskID,
			Title:       title,
			Number:      number,
			Description: description,
			StartDate:   nil,
			EndDate:     endDate,
			Status:      status,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return grouped, nil
}

func (r *TaskRepository) ListAssistantBoardTasks(ctx context.Context, boardID uuid.UUID, assigneeID *int64, dateFrom, dateTo *time.Time) ([]domain.AssistantBoardUserTasksItem, error) {
	query := `
		WITH task_closed AS (
			SELECT task_id, MIN(closed_at) AS closed_at
			FROM (
				SELECT th.task_id, th.created_at AS closed_at
				FROM task_history th
				WHERE th.action = $2
				  AND th.field_name = 'status'
				  AND th.new_value = $3

				UNION ALL

				SELECT th.task_id, th.created_at AS closed_at
				FROM task_history th
				JOIN kanban_columns c ON c.id = th.new_value::uuid
				WHERE th.action = $4
				  AND c.key = $5
			) closed_events
			GROUP BY task_id
		)
		SELECT
			u.id,
			COALESCE(NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.username) AS fio,
			COALESCE(bm.role, '') AS role,
			bm.team_role,
			t.id,
			t.task_number,
			t.title,
			t.status,
			t.created_at,
			tc.closed_at
		FROM tasks t
		JOIN task_assignees ta ON ta.task_id = t.id
		JOIN users u ON u.id = ta.user_id
		LEFT JOIN board_members bm ON bm.board_id = t.board_id AND bm.user_id = u.id
		LEFT JOIN task_closed tc ON tc.task_id = t.id
		WHERE t.board_id = $1
		  AND t.deleted_at IS NULL
	`
	args := []any{boardID, domain.ActionTaskUpdated, domain.StatusDone, domain.ActionTaskMoved, domain.ColDone}
	if assigneeID != nil {
		query += fmt.Sprintf(` AND u.id = $%d`, len(args)+1)
		args = append(args, *assigneeID)
	}
	if dateFrom != nil {
		query += fmt.Sprintf(` AND (t.created_at >= $%d OR (tc.closed_at IS NOT NULL AND tc.closed_at >= $%d))`, len(args)+1, len(args)+1)
		args = append(args, *dateFrom)
	}
	if dateTo != nil {
		query += fmt.Sprintf(` AND (t.created_at <= $%d OR (tc.closed_at IS NOT NULL AND tc.closed_at <= $%d))`, len(args)+1, len(args)+1)
		args = append(args, *dateTo)
	}
	query += ` ORDER BY fio ASC, t.created_at DESC, t.task_number DESC`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grouped := make([]domain.AssistantBoardUserTasksItem, 0)
	indexByUserID := make(map[int64]int)

	for rows.Next() {
		var (
			userID    int64
			fio       string
			role      string
			teamRole  *string
			taskID    uuid.UUID
			number    int
			title     string
			status    string
			createdAt time.Time
			closedAt  *time.Time
		)
		if err := rows.Scan(&userID, &fio, &role, &teamRole, &taskID, &number, &title, &status, &createdAt, &closedAt); err != nil {
			return nil, err
		}

		idx, exists := indexByUserID[userID]
		if !exists {
			grouped = append(grouped, domain.AssistantBoardUserTasksItem{
				User: domain.AssistantBoardUser{
					ID:       userID,
					FIO:      fio,
					Role:     role,
					TeamRole: teamRole,
				},
				Tasks: make([]domain.AssistantTask, 0),
			})
			idx = len(grouped) - 1
			indexByUserID[userID] = idx
		}

		grouped[idx].Tasks = append(grouped[idx].Tasks, domain.AssistantTask{
			ID:        taskID,
			Number:    number,
			Title:     title,
			Status:    status,
			CreatedAt: createdAt,
			ClosedAt:  closedAt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return grouped, nil
}

func (r *TaskRepository) AddAttachment(ctx context.Context, att *domain.TaskAttachment) error {
	if att.ID == uuid.Nil {
		att.ID = uuid.New()
	}
	att.CreatedAt = time.Now()
	_, err := r.mutateTaskContent(ctx, att.UploadedBy, "attachment_created", "attachment", `INSERT INTO task_attachments(id,task_id,uploaded_by,created_at,file_name,object_name,file_size,mime_type,url,size_bytes)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING task_id,NULL::text,to_jsonb(task_attachments)::text`, att.ID, att.TaskID, att.UploadedBy, att.CreatedAt, att.FileName, att.ObjectName, att.FileSize, att.MimeType, att.ObjectName, att.FileSize)
	return err
}

func (r *TaskRepository) GetAttachments(ctx context.Context, taskID uuid.UUID) ([]*domain.TaskAttachment, error) {
	query := `
        SELECT id, task_id, uploaded_by, created_at, file_name, object_name, file_size, mime_type
        FROM task_attachments
        WHERE task_id = $1
        ORDER BY created_at DESC
    `
	rows, err := r.db.Query(ctx, query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attachments := []*domain.TaskAttachment{}
	for rows.Next() {
		att := &domain.TaskAttachment{}
		if err := rows.Scan(
			&att.ID, &att.TaskID, &att.UploadedBy, &att.CreatedAt,
			&att.FileName, &att.ObjectName, &att.FileSize, &att.MimeType,
		); err != nil {
			return nil, err
		}
		att.DownloadURL = fmt.Sprintf("/api/v1/kanban/tasks/%s/files/%s/download", att.TaskID, att.ID)
		attachments = append(attachments, att)
	}
	return attachments, rows.Err()
}

func (r *TaskRepository) Update(ctx context.Context, userID int64, taskID uuid.UUID, patch *domain.UpdateTaskPatch) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	oldTask, err := r.scanTaskRow(tx.QueryRow(ctx, `SELECT `+taskSelectColumns+` FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID))
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT user_id FROM task_assignees WHERE task_id=$1 ORDER BY user_id`, taskID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		oldTask.AssigneeIDs = append(oldTask.AssigneeIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	q := `UPDATE tasks SET updated_at = NOW()`
	args := []any{taskID}
	ctr := 2

	if patch.ColumnID != nil && *patch.ColumnID != oldTask.ColumnID {
		q += fmt.Sprintf(", column_id = $%d", ctr)
		args = append(args, *patch.ColumnID)
		ctr++
		if err = r.addHistoryInTx(ctx, tx, &domain.TaskHistory{TaskID: taskID, UserID: userID, Action: domain.ActionTaskMoved, OldValue: strPtr(oldTask.ColumnID.String()), NewValue: strPtr(patch.ColumnID.String()), Metadata: map[string]interface{}{"from_column": oldTask.ColumnID.String(), "to_column": patch.ColumnID.String()}}); err != nil {
			return err
		}
	}
	// Логирование изменений полей
	now := time.Now()

	if patch.Title != nil && *patch.Title != oldTask.Title {
		q += fmt.Sprintf(`, title = $%d`, ctr)
		args = append(args, *patch.Title)
		ctr++

		if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
			TaskID:    taskID,
			UserID:    userID, // Use actual actor
			Action:    domain.ActionTaskUpdated,
			FieldName: strPtr("title"),
			OldValue:  strPtr(oldTask.Title),
			NewValue:  patch.Title,
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}

	if patch.ClearDescription && oldTask.Description != nil {
		q += `, description = NULL`
		if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
			TaskID: taskID, UserID: userID, Action: domain.ActionTaskUpdated,
			FieldName: strPtr("description"), OldValue: oldTask.Description, CreatedAt: now,
		}); err != nil {
			return err
		}
	} else if patch.Description != nil {
		oldDesc := ""
		if oldTask.Description != nil {
			oldDesc = *oldTask.Description
		}
		newDesc := *patch.Description

		if oldDesc != newDesc {
			q += fmt.Sprintf(`, description = $%d`, ctr)
			args = append(args, *patch.Description)
			ctr++

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("description"),
				OldValue:  strPtr(oldDesc),
				NewValue:  patch.Description,
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}

	if patch.Priority != nil && *patch.Priority != oldTask.Priority {
		q += fmt.Sprintf(`, priority = $%d`, ctr)
		args = append(args, *patch.Priority)
		ctr++

		if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
			TaskID:    taskID,
			UserID:    userID,
			Action:    domain.ActionTaskUpdated,
			FieldName: strPtr("priority"),
			OldValue:  strPtr(oldTask.Priority),
			NewValue:  patch.Priority,
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}

	if patch.ClearDueAt && oldTask.DueAt != nil {
		q += `, due_at = NULL`
		if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
			TaskID: taskID, UserID: userID, Action: domain.ActionTaskUpdated,
			FieldName: strPtr("due_at"), OldValue: strPtr(oldTask.DueAt.Format(time.RFC3339)), CreatedAt: now,
		}); err != nil {
			return err
		}
	} else if patch.DueAt != nil {
		oldDue := ""
		if oldTask.DueAt != nil {
			oldDue = oldTask.DueAt.Format(time.RFC3339)
		}
		newDue := patch.DueAt.Format(time.RFC3339)

		if oldDue != newDue {
			q += fmt.Sprintf(`, due_at = $%d`, ctr)
			args = append(args, *patch.DueAt)
			ctr++

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("due_at"),
				OldValue:  strPtr(oldDue),
				NewValue:  strPtr(newDue),
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}

	if patch.SprintID != nil {
		oldSprint := ""
		if oldTask.SprintID != nil {
			oldSprint = oldTask.SprintID.String()
		}
		newSprint := patch.SprintID.String()
		if *patch.SprintID == uuid.Nil {
			newSprint = ""
		}

		if oldSprint != newSprint {
			if *patch.SprintID == uuid.Nil {
				q += `, sprint_id = NULL`
			} else {
				q += fmt.Sprintf(`, sprint_id = $%d`, ctr)
				args = append(args, *patch.SprintID)
				ctr++
			}

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("sprint_id"),
				OldValue:  strPtr(oldSprint),
				NewValue:  strPtr(newSprint),
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}

	if patch.GoalID != nil {
		oldGoal := ""
		if oldTask.GoalID != nil {
			oldGoal = oldTask.GoalID.String()
		}
		newGoal := patch.GoalID.String()
		if *patch.GoalID == uuid.Nil {
			newGoal = ""
		}

		if oldGoal != newGoal {
			if *patch.GoalID == uuid.Nil {
				q += `, goal_id = NULL`
			} else {
				q += fmt.Sprintf(`, goal_id = $%d`, ctr)
				args = append(args, *patch.GoalID)
				ctr++
			}

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("goal_id"),
				OldValue:  strPtr(oldGoal),
				NewValue:  strPtr(newGoal),
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}

	if patch.ProjectID != nil {
		oldProject := ""
		if oldTask.ProjectID != nil {
			oldProject = oldTask.ProjectID.String()
		}
		newProject := patch.ProjectID.String()
		if *patch.ProjectID == uuid.Nil {
			newProject = ""
		}

		if oldProject != newProject {
			if *patch.ProjectID == uuid.Nil {
				q += `, project_id = NULL`
			} else {
				q += fmt.Sprintf(`, project_id = $%d`, ctr)
				args = append(args, *patch.ProjectID)
				ctr++
			}

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("project_id"),
				OldValue:  strPtr(oldProject),
				NewValue:  strPtr(newProject),
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}

	if patch.OriginalEstimateSec != nil && *patch.OriginalEstimateSec != oldTask.OriginalEstimateSec {
		q += fmt.Sprintf(`, original_estimate_sec = $%d`, ctr)
		args = append(args, *patch.OriginalEstimateSec)
		ctr++

		if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
			TaskID:    taskID,
			UserID:    userID,
			Action:    domain.ActionTaskUpdated,
			FieldName: strPtr("original_estimate_sec"),
			OldValue:  strPtr(fmt.Sprintf("%d", oldTask.OriginalEstimateSec)),
			NewValue:  strPtr(fmt.Sprintf("%d", *patch.OriginalEstimateSec)),
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}

	if patch.ParentID != nil {
		oldParent := ""
		if oldTask.ParentID != nil {
			oldParent = oldTask.ParentID.String()
		}
		newParent := patch.ParentID.String()

		if oldParent != newParent {
			q += fmt.Sprintf(`, parent_id = $%d`, ctr)
			args = append(args, *patch.ParentID)
			ctr++

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("parent_id"),
				OldValue:  strPtr(oldParent),
				NewValue:  strPtr(newParent),
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	} else if patch.ClearParentID && oldTask.ParentID != nil {
		oldParent := oldTask.ParentID.String()
		q += `, parent_id = NULL`

		if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
			TaskID:    taskID,
			UserID:    userID,
			Action:    domain.ActionTaskUpdated,
			FieldName: strPtr("parent_id"),
			OldValue:  strPtr(oldParent),
			NewValue:  strPtr(""),
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}

	if patch.AssigneeIDs != nil {
		var firstAssignee any
		if len(patch.AssigneeIDs) > 0 {
			firstAssignee = patch.AssigneeIDs[0]
		}
		q += fmt.Sprintf(`, assignee_id = $%d`, ctr)
		args = append(args, firstAssignee)
		ctr++
	}

	if patch.Status != nil {
		newStatus := strings.TrimSpace(*patch.Status)
		if newStatus != oldTask.Status {
			q += fmt.Sprintf(`, status = $%d`, ctr)
			args = append(args, newStatus)

			if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
				TaskID:    taskID,
				UserID:    userID,
				Action:    domain.ActionTaskUpdated,
				FieldName: strPtr("status"),
				OldValue:  strPtr(oldTask.Status),
				NewValue:  strPtr(newStatus),
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}

	q += ` WHERE id = $1`

	if len(args) > 1 || patch.ClearDueAt || patch.ClearDescription || patch.ClearParentID || patch.AssigneeIDs != nil || patch.GoalID != nil || patch.ProjectID != nil || patch.SprintID != nil {
		_, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return err
		}
	}

	// Обработка изменения исполнителей
	if patch.AssigneeIDs != nil {
		// Получаем старых исполнителей
		oldAssignees := make(map[int64]bool)
		for _, id := range oldTask.AssigneeIDs {
			oldAssignees[id] = true
		}

		// Новые исполнители
		newAssignees := make(map[int64]bool)
		for _, id := range patch.AssigneeIDs {
			newAssignees[id] = true
		}

		// Находим удаленных исполнителей
		for oldID := range oldAssignees {
			if !newAssignees[oldID] {
				if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
					TaskID:    taskID,
					UserID:    userID,
					Action:    domain.ActionTaskUnassigned,
					OldValue:  strPtr(fmt.Sprintf("%d", oldID)),
					CreatedAt: now,
				}); err != nil {
					return err
				}
			}
		}

		// Находим новых исполнителей
		for newID := range newAssignees {
			if !oldAssignees[newID] {
				if err := r.addHistoryInTx(ctx, tx, &domain.TaskHistory{
					TaskID:    taskID,
					UserID:    userID,
					Action:    domain.ActionTaskAssigned,
					NewValue:  strPtr(fmt.Sprintf("%d", newID)),
					CreatedAt: now,
				}); err != nil {
					return err
				}
			}
		}

		_, err := tx.Exec(ctx, `DELETE FROM task_assignees WHERE task_id=$1`, taskID)
		if err != nil {
			return err
		}
		for _, uid := range uniqueInt64(patch.AssigneeIDs) {
			_, err := tx.Exec(ctx, `INSERT INTO task_assignees (task_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, taskID, uid)
			if err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

func (r *TaskRepository) Move(ctx context.Context, userID int64, taskID, columnID uuid.UUID, newStatus string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var oldColumn uuid.UUID
	var oldStatus string
	if err = tx.QueryRow(ctx, `SELECT column_id,status FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&oldColumn, &oldStatus); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE tasks SET column_id=$1,status=$2,updated_at=NOW() WHERE id=$3`, columnID, newStatus, taskID); err != nil {
		return err
	}
	if err = r.addHistoryInTx(ctx, tx, &domain.TaskHistory{TaskID: taskID, UserID: userID, Action: domain.ActionTaskMoved, OldValue: strPtr(oldColumn.String()), NewValue: strPtr(columnID.String()), Metadata: map[string]interface{}{"from_column": oldColumn.String(), "to_column": columnID.String(), "old_status": oldStatus, "new_status": newStatus}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *TaskRepository) Delete(ctx context.Context, userID int64, taskID uuid.UUID) error {
	_, err := r.mutateTaskContent(ctx, userID, domain.ActionTaskDeleted, "task", `WITH old AS (SELECT * FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE) UPDATE tasks t SET deleted_at=NOW(),updated_at=NOW() FROM old WHERE t.id=old.id RETURNING t.id,to_jsonb(old)::text,to_jsonb(t)::text`, taskID)
	return err
}

func (r *TaskRepository) GetByID(ctx context.Context, taskID uuid.UUID) (*domain.Task, error) {
	t, err := r.scanTaskRow(r.db.QueryRow(ctx, `
        SELECT `+taskSelectColumns+`
        FROM tasks 
        WHERE id=$1 AND deleted_at IS NULL
    `, taskID))
	if err != nil {
		return nil, err
	}

	assignees, err := r.GetAssignees(ctx, t.ID)
	if err == nil {
		t.AssigneeIDs = assignees
	}

	attachments, err := r.GetAttachments(ctx, t.ID)
	if err == nil {
		t.Attachments = attachments
	}

	// Fetch subtasks
	subtasks, _ := r.GetSubtasks(ctx, t.ID)
	t.Subtasks = subtasks

	// Fetch labels
	t.Labels, _ = r.GetLabels(ctx, t.ID)

	return t, nil
}

func (r *TaskRepository) GetByTaskKey(ctx context.Context, userID int64, taskKey string) (*domain.Task, error) {
	t, err := r.scanTaskRow(r.db.QueryRow(ctx, `
        SELECT `+taskSelectColumnsWithAlias+`
        FROM tasks t
        LEFT JOIN task_assignees ta ON t.id = ta.task_id
        WHERE UPPER(t.task_key) = UPPER($1)
          AND COALESCE(to_jsonb(t)->>'source_system','')<>'documentolog'
          AND t.deleted_at IS NULL
          AND (
              t.created_by = $2
              OR ta.user_id = $2
              OR EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = t.board_id AND bm.user_id = $2)
              OR `+kanbanExplicitBoardAccessExists(`t.board_id`, `$2`)+`
              OR `+kanbanLeadershipBoardAccessExists(`t.board_id`, `$2`)+`
          )
        ORDER BY t.created_at DESC
        LIMIT 1
    `, strings.TrimSpace(taskKey), userID))
	if err != nil {
		return nil, err
	}

	assignees, err := r.GetAssignees(ctx, t.ID)
	if err == nil {
		t.AssigneeIDs = assignees
	}

	attachments, err := r.GetAttachments(ctx, t.ID)
	if err == nil {
		t.Attachments = attachments
	}

	subtasks, _ := r.GetSubtasks(ctx, t.ID)
	t.Subtasks = subtasks

	t.Labels, _ = r.GetLabels(ctx, t.ID)

	return t, nil
}

func (r *TaskRepository) scanTaskRow(row pgx.Row) (*domain.Task, error) {
	t := &domain.Task{}
	if err := row.Scan(
		&t.ID,
		&t.BoardID,
		&t.GoalID,
		&t.ProjectID,
		&t.SprintID,
		&t.ColumnID,
		&t.CreatedBy,
		&t.TaskNumber,
		&t.TaskKey,
		&t.Title,
		&t.Description,
		&t.Priority,
		&t.DueAt,
		&t.OriginalEstimateSec,
		&t.TimeSpentSec,
		&t.CreatedAt,
		&t.UpdatedAt,
		&t.ParentID,
		&t.Status,
	); err != nil {
		return nil, err
	}
	setTaskLink(t)
	return t, nil
}

func (r *TaskRepository) GetSubtasks(ctx context.Context, parentID uuid.UUID) ([]*domain.Task, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+taskSelectColumns+`
        FROM tasks 
        WHERE parent_id=$1 AND deleted_at IS NULL
	`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTasks(ctx, rows)
}

// GetTasksByColumnKeys is for BOARD view (UUID)
func (r *TaskRepository) GetTasksByColumnKeys(ctx context.Context, boardID uuid.UUID, keys []string) ([]*domain.Task, error) {
	q := `
		SELECT ` + taskSelectColumnsWithAlias + `
		FROM tasks t
		JOIN kanban_columns c ON t.column_id = c.id
		WHERE t.board_id = $1 AND c.key = ANY($2) AND t.deleted_at IS NULL
	`

	rows, err := r.db.Query(ctx, q, boardID, keys)
	if err != nil {
		return nil, fmt.Errorf("query tasks by keys: %w", err)
	}
	defer rows.Close()

	return r.scanTasks(ctx, rows)
}

// GetTasksByUserAndColumnKeys is for CALENDAR view (int64) - Fixes type mismatch error
func (r *TaskRepository) GetTasksByUserAndColumnKeys(ctx context.Context, userID int64, keys []string) ([]*domain.Task, error) {
	q := `
		SELECT DISTINCT ` + taskSelectColumnsWithAlias + `, COALESCE(c.key, '')
		FROM tasks t
		JOIN kanban_columns c ON t.column_id = c.id
		JOIN task_assignees ta ON t.id = ta.task_id
		JOIN boards b ON t.board_id = b.id
		WHERE ta.user_id = $1
		AND c.key = ANY($2)
		AND COALESCE(to_jsonb(t)->>'source_system','')<>'documentolog'
		AND t.deleted_at IS NULL
		AND ` + kanbanBoardVisibilityCondition("b", "$1") + `
	`

	rows, err := r.db.Query(ctx, q, userID, keys)
	if err != nil {
		return nil, fmt.Errorf("query user tasks by keys: %w", err)
	}
	defer rows.Close()

	return scanTasksWithColumnKey(ctx, r, rows)
}

// ListDocumentologAssignmentsByAssignee returns only Documentolog assignments
// addressed to the current user. Unlike the general board list, this does not
// grant visibility through board membership or ownership.
func (r *TaskRepository) ListDocumentologAssignmentsByAssignee(ctx context.Context, userID int64) ([]*domain.Task, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT `+taskSelectColumnsWithAlias+`,
			COALESCE(t.source_system, ''), COALESCE(t.source_assignment_id, ''), COALESCE(t.source_document_id, ''),
			COALESCE(t.source_url, ''), COALESCE(t.source_author_sub, ''), COALESCE(t.source_department_id, ''),
			t.source_organization_id,t.source_revision,COALESCE(t.source_status,''),t.source_due_date,COALESCE(t.source_timezone,''),t.source_completed_at,t.source_closed_at
		FROM tasks t
		JOIN task_assignees ta ON ta.task_id = t.id
		JOIN users odo_viewer ON odo_viewer.id=ta.user_id AND odo_viewer.is_active AND odo_viewer.deleted_at IS NULL
		JOIN boards odo_board ON odo_board.id=t.board_id AND odo_board.deleted_at IS NULL
		 AND odo_board.system_key='documentolog-assignments:'||odo_viewer.organization_id::text
		WHERE ta.user_id = $1
		  AND t.source_system = 'documentolog'
		  AND t.source_organization_id=odo_viewer.organization_id
		  AND t.deleted_at IS NULL
		ORDER BY t.due_at NULLS LAST, t.created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list Documentolog assignments by assignee: %w", err)
	}
	defer rows.Close()

	return r.scanDocumentologAssignmentTasks(ctx, rows)
}

func (r *TaskRepository) scanDocumentologAssignmentTasks(ctx context.Context, rows pgx.Rows) ([]*domain.Task, error) {
	var tasks []*domain.Task
	for rows.Next() {
		t := &domain.Task{}
		if err := rows.Scan(
			&t.ID, &t.BoardID, &t.GoalID, &t.ProjectID, &t.SprintID, &t.ColumnID, &t.CreatedBy, &t.TaskNumber, &t.TaskKey, &t.Title, &t.Description,
			&t.Priority, &t.DueAt, &t.OriginalEstimateSec, &t.TimeSpentSec, &t.CreatedAt, &t.UpdatedAt, &t.ParentID, &t.Status,
			&t.SourceSystem, &t.SourceAssignmentID, &t.SourceDocumentID, &t.SourceURL, &t.SourceAuthorSub, &t.SourceDepartmentID,
			&t.SourceOrganizationID, &t.SourceRevision, &t.SourceStatus, &t.SourceDueDate, &t.SourceTimezone, &t.SourceCompletedAt, &t.SourceClosedAt,
		); err != nil {
			return nil, err
		}
		setTaskLink(t)
		t.AssigneeIDs, _ = r.GetAssignees(ctx, t.ID)
		t.Attachments, _ = r.GetAttachments(ctx, t.ID)
		t.Labels, _ = r.GetLabels(ctx, t.ID)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (r *TaskRepository) scanTasks(ctx context.Context, rows pgx.Rows) ([]*domain.Task, error) {
	tasks, err := scanTaskRowsBase(rows)
	if err != nil {
		return nil, err
	}

	for _, t := range tasks {
		t.AssigneeIDs, _ = r.GetAssignees(ctx, t.ID)
		t.Attachments, _ = r.GetAttachments(ctx, t.ID)
		t.Labels, _ = r.GetLabels(ctx, t.ID)
	}
	return tasks, nil
}

func scanTaskRowsBase(rows pgx.Rows) ([]*domain.Task, error) {
	var tasks []*domain.Task
	for rows.Next() {
		t := &domain.Task{}
		if err := rows.Scan(
			&t.ID, &t.BoardID, &t.GoalID, &t.ProjectID, &t.SprintID, &t.ColumnID, &t.CreatedBy, &t.TaskNumber, &t.TaskKey, &t.Title, &t.Description,
			&t.Priority, &t.DueAt, &t.OriginalEstimateSec, &t.TimeSpentSec, &t.CreatedAt, &t.UpdatedAt, &t.ParentID, &t.Status,
		); err != nil {
			return nil, err
		}
		setTaskLink(t)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func scanTasksWithColumnKey(ctx context.Context, r *TaskRepository, rows pgx.Rows) ([]*domain.Task, error) {
	var tasks []*domain.Task
	for rows.Next() {
		t := &domain.Task{}
		if err := rows.Scan(
			&t.ID, &t.BoardID, &t.GoalID, &t.ProjectID, &t.SprintID, &t.ColumnID, &t.CreatedBy, &t.TaskNumber, &t.TaskKey, &t.Title, &t.Description,
			&t.Priority, &t.DueAt, &t.OriginalEstimateSec, &t.TimeSpentSec, &t.CreatedAt, &t.UpdatedAt, &t.ParentID, &t.Status, &t.ColumnKey,
		); err != nil {
			return nil, err
		}
		setTaskLink(t)
		t.AssigneeIDs, _ = r.GetAssignees(ctx, t.ID)
		t.Attachments, _ = r.GetAttachments(ctx, t.ID)
		t.Labels, _ = r.GetLabels(ctx, t.ID)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func setTaskLink(t *domain.Task) {
	if t == nil {
		return
	}
	if t.TaskKey != "" {
		t.Link = fmt.Sprintf("/kanban?task=%s", t.TaskKey)
		return
	}
	t.Link = fmt.Sprintf("/kanban?task=%s", t.ID)
}

func (r *TaskRepository) GetAttachmentByID(ctx context.Context, attachmentID uuid.UUID) (*domain.TaskAttachment, error) {
	att := &domain.TaskAttachment{}
	err := r.db.QueryRow(ctx, `
        SELECT id, task_id, uploaded_by, created_at, file_name, object_name, file_size, mime_type
        FROM task_attachments WHERE id = $1
    `, attachmentID).Scan(
		&att.ID, &att.TaskID, &att.UploadedBy, &att.CreatedAt,
		&att.FileName, &att.ObjectName, &att.FileSize, &att.MimeType,
	)
	if err != nil {
		return nil, err
	}
	att.DownloadURL = fmt.Sprintf("/api/v1/kanban/tasks/%s/files/%s/download", att.TaskID, att.ID)
	return att, nil
}

func (r *TaskRepository) DeleteAttachment(ctx context.Context, attachmentID uuid.UUID, userID int64) error {
	_, err := r.mutateTaskContent(ctx, userID, "attachment_deleted", "attachment", `DELETE FROM task_attachments WHERE id=$1 RETURNING task_id,to_jsonb(task_attachments)::text,NULL::text`, attachmentID)
	return err
}

func (r *TaskRepository) AddWorklog(ctx context.Context, w *domain.TaskWorklog) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now()
	}

	// 1. Insert worklog
	_, err = tx.Exec(ctx, `
		INSERT INTO task_worklogs (id, task_id, user_id, time_spent_sec, started_at, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, w.ID, w.TaskID, w.UserID, w.TimeSpentSec, w.StartedAt, w.Comment, w.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert worklog: %w", err)
	}

	// 2. Update task total time_spent_sec
	_, err = tx.Exec(ctx, `
		UPDATE tasks 
		SET time_spent_sec = time_spent_sec + $1, updated_at = NOW()
		WHERE id = $2
	`, w.TimeSpentSec, w.TaskID)
	if err != nil {
		return fmt.Errorf("update task time: %w", err)
	}

	snapshot, err := json.Marshal(w)
	if err != nil {
		return err
	}
	if err = r.addHistoryInTx(ctx, tx, &domain.TaskHistory{TaskID: w.TaskID, UserID: w.UserID, Action: "worklog_added", FieldName: strPtr("worklog"), NewValue: strPtr(string(snapshot))}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *TaskRepository) ListWorklogs(ctx context.Context, taskID uuid.UUID) ([]*domain.TaskWorklog, error) {
	q := `
		SELECT id, task_id, user_id, time_spent_sec, started_at, comment, created_at
		FROM task_worklogs
		WHERE task_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(ctx, q, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*domain.TaskWorklog
	for rows.Next() {
		w := &domain.TaskWorklog{}
		err := rows.Scan(&w.ID, &w.TaskID, &w.UserID, &w.TimeSpentSec, &w.StartedAt, &w.Comment, &w.CreatedAt)
		if err != nil {
			return nil, err
		}
		logs = append(logs, w)
	}
	return logs, nil
}

// AddHistory добавляет запись в историю изменений задачи
func (r *TaskRepository) AddHistory(ctx context.Context, history *domain.TaskHistory) error {
	if history.ID == uuid.Nil {
		history.ID = uuid.New()
	}

	// Сериализуем metadata в JSONB
	var metadataJSON interface{}
	if history.Metadata != nil {
		metadataJSON = history.Metadata
	}

	// Если CreatedAt не установлен (нулевое время), используем NOW() из БД
	var createdAt interface{}
	if history.CreatedAt.IsZero() {
		createdAt = nil // COALESCE подставит NOW()
	} else {
		createdAt = history.CreatedAt
	}

	query := `
		INSERT INTO task_history 
		(id, task_id, user_id, action, field_name, old_value, new_value, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()))
	`

	_, err := r.db.Exec(ctx, query,
		history.ID,
		history.TaskID,
		history.UserID,
		history.Action,
		history.FieldName,
		history.OldValue,
		history.NewValue,
		metadataJSON,
		createdAt, // Используем переменную вместо напрямую history.CreatedAt
	)

	if err != nil {
		r.logger.Error("Failed to add task history",
			zap.Error(err),
			zap.String("task_id", history.TaskID.String()),
			zap.String("action", history.Action),
		)
		return fmt.Errorf("add task history: %w", err)
	}

	return nil
}

// GetHistory получает всю историю изменений задачи
func (r *TaskRepository) GetHistory(ctx context.Context, taskID uuid.UUID) ([]*domain.TaskHistoryResponse, error) {
	query := `
		SELECT
			th.id,
			th.task_id,
			th.user_id,
			COALESCE(
				NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''),
				NULLIF(u.username, ''),
				'Unknown user'
			) AS user_name,
			th.action,
			th.field_name,
			th.old_value,
			th.new_value,
			th.metadata,
			th.created_at
		FROM task_history th
		LEFT JOIN users u ON u.id = th.user_id
		WHERE th.task_id = $1
		ORDER BY th.created_at DESC, th.id DESC
	`

	rows, err := r.db.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task history: %w", err)
	}
	defer rows.Close()

	history := []*domain.TaskHistoryResponse{}
	for rows.Next() {
		h := &domain.TaskHistoryResponse{}
		err := rows.Scan(
			&h.ID,
			&h.TaskID,
			&h.UserID,
			&h.UserName,
			&h.Action,
			&h.FieldName,
			&h.OldValue,
			&h.NewValue,
			&h.Metadata,
			&h.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		history = append(history, h)
	}

	return history, rows.Err()
}

func (r *TaskRepository) CreateComment(ctx context.Context, c *domain.TaskComment) error {
	_, err := r.mutateTaskContent(ctx, c.UserID, domain.ActionCommentAdded, "comment", `INSERT INTO task_comments(id,task_id,user_id,content,attachment_url,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING task_id,NULL::text,to_jsonb(task_comments)::text`, c.ID, c.TaskID, c.UserID, c.Content, c.AttachmentURL, c.CreatedAt, c.UpdatedAt)
	return err
}

func (r *TaskRepository) GetComments(ctx context.Context, taskID uuid.UUID) ([]*domain.TaskComment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.task_id, c.user_id, c.content, c.attachment_url, c.created_at, c.updated_at,
		       u.username, u.first_name, u.last_name, u.avatar_url
		FROM task_comments c
		JOIN users u ON c.user_id = u.id
		WHERE c.task_id = $1 AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comments := []*domain.TaskComment{}
	for rows.Next() {
		com := &domain.TaskComment{Author: &domain.UserResponse{}}
		err := rows.Scan(
			&com.ID, &com.TaskID, &com.UserID, &com.Content, &com.AttachmentURL, &com.CreatedAt, &com.UpdatedAt,
			&com.Author.Username, &com.Author.FirstName, &com.Author.LastName, &com.Author.AvatarURL,
		)
		if err != nil {
			return nil, err
		}
		com.Author.ID = com.UserID
		comments = append(comments, com)
	}
	return comments, rows.Err()
}

func (r *TaskRepository) DeleteComment(ctx context.Context, commentID uuid.UUID, userID int64) error {
	_, err := r.mutateTaskContent(ctx, userID, "comment_deleted", "comment", `WITH old AS (SELECT * FROM task_comments WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE) UPDATE task_comments c SET deleted_at=NOW(),updated_at=NOW() FROM old WHERE c.id=old.id RETURNING c.task_id,to_jsonb(old)::text,to_jsonb(c)::text`, commentID, userID)
	return err
}

func (r *TaskRepository) UpdateComment(ctx context.Context, commentID uuid.UUID, userID int64, content string) error {
	_, err := r.mutateTaskContent(ctx, userID, "comment_updated", "comment", `WITH old AS (SELECT * FROM task_comments WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE) UPDATE task_comments c SET content=$3,updated_at=NOW() FROM old WHERE c.id=old.id RETURNING c.task_id,to_jsonb(old)::text,to_jsonb(c)::text`, commentID, userID, content)
	return err
}

func (r *TaskRepository) GetCommentByID(ctx context.Context, commentID uuid.UUID) (*domain.TaskComment, error) {
	com := &domain.TaskComment{}
	err := r.db.QueryRow(ctx, `
		SELECT id, task_id, user_id, content, attachment_url, created_at, updated_at
		FROM task_comments WHERE id = $1 AND deleted_at IS NULL
	`, commentID).Scan(&com.ID, &com.TaskID, &com.UserID, &com.Content, &com.AttachmentURL, &com.CreatedAt, &com.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return com, nil
}

// Labels

func (r *TaskRepository) AddLabel(ctx context.Context, taskID, labelID uuid.UUID, userID int64) error {
	_, err := r.mutateTaskContent(ctx, userID, "label_added", "label", `INSERT INTO kanban_task_labels(task_id,label_id,assigned_at) VALUES($1,$2,NOW()) ON CONFLICT DO NOTHING RETURNING task_id,NULL::text,to_jsonb(kanban_task_labels)::text`, taskID, labelID)
	if err == pgx.ErrNoRows {
		return nil
	}
	return err
}

func (r *TaskRepository) RemoveLabel(ctx context.Context, taskID, labelID uuid.UUID, userID int64) error {
	_, err := r.mutateTaskContent(ctx, userID, "label_removed", "label", `DELETE FROM kanban_task_labels WHERE task_id=$1 AND label_id=$2 RETURNING task_id,to_jsonb(kanban_task_labels)::text,NULL::text`, taskID, labelID)
	if err == pgx.ErrNoRows {
		return nil
	}
	return err
}

func (r *TaskRepository) GetLabels(ctx context.Context, taskID uuid.UUID) ([]*domain.KanbanLabel, error) {
	rows, err := r.db.Query(ctx, `
		SELECT l.id, l.board_id, l.name, l.color, l.created_at
		FROM kanban_labels l
		JOIN kanban_task_labels tl ON l.id = tl.label_id
		WHERE tl.task_id = $1
		ORDER BY l.name
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []*domain.KanbanLabel
	for rows.Next() {
		l := &domain.KanbanLabel{}
		if err := rows.Scan(&l.ID, &l.BoardID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, nil
}

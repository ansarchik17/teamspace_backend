package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *TaskRepository) Search(ctx context.Context, userID int64, f domain.TaskFilters) ([]*domain.Task, error) {
	return r.searchWithScanner(ctx, userID, f, r.scanSearchTasks)
}

func (r *TaskRepository) SearchLightweight(ctx context.Context, userID int64, f domain.TaskFilters) ([]*domain.Task, error) {
	return r.searchWithScanner(ctx, userID, f, func(_ context.Context, rows pgx.Rows) ([]*domain.Task, error) {
		return scanTaskRowsBase(rows)
	})
}

func (r *TaskRepository) searchWithScanner(ctx context.Context, userID int64, f domain.TaskFilters, scan func(context.Context, pgx.Rows) ([]*domain.Task, error)) ([]*domain.Task, error) {
	query := `
       SELECT
          ` + taskSelectColumnsWithAlias + `
       FROM tasks t
       LEFT JOIN kanban_columns c ON t.column_id = c.id
       JOIN boards b ON t.board_id = b.id
       WHERE t.deleted_at IS NULL
         AND ` + kanbanBoardVisibilityCondition("b", "$1") + `
    `

	args := []any{userID}
	ctr := 2
	conditions := []string{}

	if f.Query != "" {
		conditions = append(conditions, fmt.Sprintf("(t.title ILIKE $%d OR t.description ILIKE $%d OR t.task_key ILIKE $%d OR t.status ILIKE $%d)", ctr, ctr, ctr, ctr))
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.Query)+"%")
		ctr++
	}

	if len(f.AuthorIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("t.created_by = ANY($%d)", ctr))
		args = append(args, f.AuthorIDs)
		ctr++
	}
	if len(f.Statuses) > 0 {
		conditions = append(conditions, fmt.Sprintf("(UPPER(t.status) = ANY($%d) OR UPPER(c.key) = ANY($%d) OR UPPER(c.name) = ANY($%d))", ctr, ctr, ctr))
		args = append(args, f.Statuses)
		ctr++
	}

	if len(f.AssigneeIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id = t.id AND ta.user_id = ANY($%d))", ctr))
		args = append(args, f.AssigneeIDs)
		ctr++
	}

	if len(f.Priorities) > 0 {
		conditions = append(conditions, fmt.Sprintf("LOWER(t.priority) = ANY($%d)", ctr))
		args = append(args, f.Priorities)
		ctr++
	}

	if len(f.ColumnIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("t.column_id = ANY($%d)", ctr))
		args = append(args, f.ColumnIDs)
		ctr++
	}

	if len(f.ColumnKeys) > 0 {
		conditions = append(conditions, fmt.Sprintf("c.key = ANY($%d)", ctr))
		args = append(args, f.ColumnKeys)
		ctr++
	}

	if f.DateFrom != nil {
		conditions = append(conditions, fmt.Sprintf("t.due_at >= $%d", ctr))
		args = append(args, *f.DateFrom)
		ctr++
	}
	if f.DateTo != nil {
		conditions = append(conditions, fmt.Sprintf("t.due_at <= $%d", ctr))
		args = append(args, *f.DateTo)
		ctr++
	}

	if f.BoardID != nil {
		conditions = append(conditions, fmt.Sprintf("t.board_id = $%d", ctr))
		args = append(args, *f.BoardID)
		ctr++
	}

	if len(f.LabelIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM kanban_task_labels ktl WHERE ktl.task_id = t.id AND ktl.label_id = ANY($%d))", ctr))
		args = append(args, f.LabelIDs)
		ctr++
	}

	if len(f.SprintIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("t.sprint_id = ANY($%d)", ctr))
		args = append(args, f.SprintIDs)
		ctr++
	}

	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY t.created_at DESC, t.id DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", ctr)
		args = append(args, f.Limit)
		ctr++
	} else {
		query += " LIMIT 100"
	}

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", ctr)
		args = append(args, f.Offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	return scan(ctx, rows)
}

func (r *TaskRepository) GetOrgProjectStats(ctx context.Context, userIDs []int64) ([]domain.OrgProjectStats, error) {
	if len(userIDs) == 0 {
		return []domain.OrgProjectStats{}, nil
	}

	query := `
		WITH relevant_boards AS (
			SELECT DISTINCT b.id, b.name
			FROM boards b
			JOIN board_members bm ON bm.board_id = b.id
			WHERE b.deleted_at IS NULL
			  AND COALESCE(to_jsonb(b)->>'system_key','')<>'documentolog-assignments'
			  AND COALESCE(to_jsonb(b)->>'system_key','') NOT LIKE 'documentolog-assignments:%'
			  AND bm.user_id = ANY($1)
		)
		SELECT rb.id, rb.name, t.status, COUNT(*)::int
		FROM relevant_boards rb
		JOIN tasks t ON t.board_id = rb.id
		WHERE t.deleted_at IS NULL
		GROUP BY rb.id, rb.name, t.status
		ORDER BY rb.name, t.status
	`

	rows, err := r.db.Query(ctx, query, userIDs)
	if err != nil {
		return nil, fmt.Errorf("get org project stats: %w", err)
	}
	defer rows.Close()

	projectMap := make(map[uuid.UUID]*domain.OrgProjectStats)
	var boardOrder []uuid.UUID

	for rows.Next() {
		var (
			boardID   uuid.UUID
			boardName string
			status    string
			count     int
		)

		if err := rows.Scan(&boardID, &boardName, &status, &count); err != nil {
			return nil, fmt.Errorf("scan org project stats: %w", err)
		}

		project, ok := projectMap[boardID]
		if !ok {
			project = &domain.OrgProjectStats{
				BoardID:   boardID,
				BoardName: boardName,
			}
			projectMap[boardID] = project
			boardOrder = append(boardOrder, boardID)
		}

		project.TotalTasks += count
		project.Statuses = append(project.Statuses, domain.OrgTaskStatusStat{
			Status: status,
			Count:  count,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate org project stats: %w", err)
	}

	stats := make([]domain.OrgProjectStats, 0, len(boardOrder))
	for _, boardID := range boardOrder {
		project := projectMap[boardID]
		sort.Slice(project.Statuses, func(i, j int) bool {
			return project.Statuses[i].Status < project.Statuses[j].Status
		})
		stats = append(stats, *project)
	}

	return stats, nil
}

func (r *TaskRepository) GetOrgClosedTasksByUser(ctx context.Context, userIDs []int64) ([]domain.OrgClosedTasksByUser, error) {
	if len(userIDs) == 0 {
		return []domain.OrgClosedTasksByUser{}, nil
	}

	query := `
		WITH relevant_boards AS (
			SELECT DISTINCT b.id
			FROM boards b
			JOIN board_members bm ON bm.board_id = b.id
			WHERE b.deleted_at IS NULL
			  AND COALESCE(to_jsonb(b)->>'system_key','')<>'documentolog-assignments'
			  AND COALESCE(to_jsonb(b)->>'system_key','') NOT LIKE 'documentolog-assignments:%'
			  AND bm.user_id = ANY($1)
		)
		SELECT
			th.user_id,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
			t.id,
			t.title,
			b.id,
			b.name,
			th.created_at
		FROM task_history th
		JOIN tasks t ON t.id = th.task_id
		JOIN boards b ON b.id = t.board_id
		JOIN kanban_columns c ON c.id = th.new_value::uuid
		LEFT JOIN users u ON u.id = th.user_id
		WHERE th.action = $2
		  AND c.key = $3
		  AND t.deleted_at IS NULL
		  AND b.id IN (SELECT id FROM relevant_boards)
		ORDER BY th.created_at DESC
	`

	rows, err := r.db.Query(ctx, query, userIDs, domain.ActionTaskMoved, domain.ColDone)
	if err != nil {
		return nil, fmt.Errorf("get org closed tasks by user: %w", err)
	}
	defer rows.Close()

	userMap := make(map[int64]*domain.OrgClosedTasksByUser)
	var userOrder []int64

	for rows.Next() {
		var (
			userID    int64
			fullName  string
			taskID    uuid.UUID
			title     string
			boardID   uuid.UUID
			boardName string
			closedAt  time.Time
		)

		if err := rows.Scan(&userID, &fullName, &taskID, &title, &boardID, &boardName, &closedAt); err != nil {
			return nil, fmt.Errorf("scan org closed task: %w", err)
		}

		entry, ok := userMap[userID]
		if !ok {
			entry = &domain.OrgClosedTasksByUser{
				UserID:   userID,
				FullName: strings.TrimSpace(fullName),
			}
			if entry.FullName == "" {
				entry.FullName = fmt.Sprintf("User %d", userID)
			}
			userMap[userID] = entry
			userOrder = append(userOrder, userID)
		}

		entry.Tasks = append(entry.Tasks, domain.OrgClosedTaskItem{
			TaskID:    taskID,
			Title:     title,
			BoardID:   boardID,
			BoardName: boardName,
			ClosedAt:  closedAt,
		})
		entry.ClosedCount++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate org closed tasks: %w", err)
	}

	result := make([]domain.OrgClosedTasksByUser, 0, len(userOrder))
	for _, userID := range userOrder {
		result = append(result, *userMap[userID])
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].ClosedCount == result[j].ClosedCount {
			return result[i].FullName < result[j].FullName
		}
		return result[i].ClosedCount > result[j].ClosedCount
	})

	return result, nil
}

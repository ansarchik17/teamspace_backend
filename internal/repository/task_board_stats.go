package repository

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *TaskRepository) GetBoardStats(ctx context.Context, boardID uuid.UUID, dateFrom, dateTo *time.Time) (*domain.BoardStatsResponse, error) {
	stats := &domain.BoardStatsResponse{
		BoardID: boardID,
		Period: domain.BoardStatsPeriod{
			DateFrom: dateFrom,
			DateTo:   dateTo,
		},
	}
	_ = r.db.QueryRow(ctx, `SELECT name FROM boards WHERE id = $1 AND deleted_at IS NULL`, boardID).Scan(&stats.BoardName)

	var fromArg any
	if dateFrom != nil {
		fromArg = *dateFrom
	}
	var toArg any
	if dateTo != nil {
		toArg = *dateTo
	}

	if err := r.scanBoardStatsSummary(ctx, boardID, fromArg, toArg, &stats.Summary); err != nil {
		return nil, err
	}

	var err error
	stats.ByStatus, err = r.listBoardStatsByStatus(ctx, boardID, fromArg, toArg, stats.Summary.TotalTasks)
	if err != nil {
		return nil, err
	}
	stats.ByColumn, err = r.listBoardStatsByColumn(ctx, boardID, fromArg, toArg, stats.Summary.TotalTasks)
	if err != nil {
		return nil, err
	}
	stats.ByPriority, err = r.listBoardStatsByPriority(ctx, boardID, fromArg, toArg, stats.Summary.TotalTasks)
	if err != nil {
		return nil, err
	}
	stats.ByDeadline, err = r.listBoardStatsByDeadline(ctx, boardID, fromArg, toArg, stats.Summary.TotalTasks)
	if err != nil {
		return nil, err
	}
	stats.Employees, err = r.listBoardStatsEmployees(ctx, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	stats.Activity, err = r.listBoardStatsActivity(ctx, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	stats.Worklogs, err = r.listBoardStatsWorklogs(ctx, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

func (r *TaskRepository) scanBoardStatsSummary(ctx context.Context, boardID uuid.UUID, fromArg, toArg any, out *domain.BoardStatsSummary) error {
	query := `
		WITH filtered AS (
			SELECT t.*, COALESCE(c.key, '') AS column_key
			FROM tasks t
			LEFT JOIN kanban_columns c ON c.id = t.column_id
			WHERE t.board_id = $1
			  AND t.deleted_at IS NULL
			  AND ($2::timestamptz IS NULL OR t.created_at >= $2::timestamptz)
			  AND ($3::timestamptz IS NULL OR t.created_at <= $3::timestamptz)
		),
		completed AS (
			SELECT task_id, MIN(created_at) AS completed_at
			FROM task_history
			WHERE action = 'updated'
			  AND field_name = 'status'
			  AND new_value = 'ГОТОВО'
			GROUP BY task_id
		)
		SELECT
			COUNT(*)::int AS total_tasks,
			COUNT(*) FILTER (WHERE f.status = 'ГОТОВО' OR f.column_key = 'DONE')::int AS completed_tasks,
			COUNT(*) FILTER (WHERE NOT (f.status = 'ГОТОВО' OR f.column_key = 'DONE'))::int AS active_tasks,
			COUNT(*) FILTER (WHERE f.due_at IS NOT NULL AND f.due_at < NOW() AND NOT (f.status = 'ГОТОВО' OR f.column_key = 'DONE'))::int AS overdue_tasks,
			COUNT(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id = f.id))::int AS unassigned_tasks,
			COALESCE(SUM(f.original_estimate_sec), 0)::bigint AS total_estimate_seconds,
			COALESCE(SUM(f.time_spent_sec), 0)::bigint AS total_spent_seconds,
			COALESCE(AVG(EXTRACT(EPOCH FROM (COALESCE(c.completed_at, f.updated_at) - f.created_at))) FILTER (WHERE f.status = 'ГОТОВО' OR f.column_key = 'DONE'), 0)::bigint AS avg_completion_seconds
		FROM filtered f
		LEFT JOIN completed c ON c.task_id = f.id
	`

	if err := r.db.QueryRow(ctx, query, boardID, fromArg, toArg).Scan(
		&out.TotalTasks,
		&out.CompletedTasks,
		&out.ActiveTasks,
		&out.OverdueTasks,
		&out.UnassignedTasks,
		&out.TotalEstimateSeconds,
		&out.TotalSpentSeconds,
		&out.AvgCompletionSeconds,
	); err != nil {
		return err
	}
	out.CompletionPercent = percent(out.CompletedTasks, out.TotalTasks)
	return nil
}

func (r *TaskRepository) listBoardStatsByStatus(ctx context.Context, boardID uuid.UUID, fromArg, toArg any, total int) ([]domain.BoardStatsCountItem, error) {
	query := `
		SELECT COALESCE(NULLIF(TRIM(status), ''), 'Без статуса') AS status, COUNT(*)::int
		FROM tasks
		WHERE board_id = $1
		  AND deleted_at IS NULL
		  AND ($2::timestamptz IS NULL OR created_at >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR created_at <= $3::timestamptz)
		GROUP BY status
		ORDER BY COUNT(*) DESC, status ASC
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.BoardStatsCountItem{}
	for rows.Next() {
		var item domain.BoardStatsCountItem
		if err := rows.Scan(&item.Key, &item.Count); err != nil {
			return nil, err
		}
		item.Name = item.Key
		item.Percent = percent(item.Count, total)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) listBoardStatsByColumn(ctx context.Context, boardID uuid.UUID, fromArg, toArg any, total int) ([]domain.BoardStatsColumnItem, error) {
	query := `
		SELECT c.id, COALESCE(c.key, ''), c.name, c.position, COUNT(t.id)::int
		FROM kanban_columns c
		LEFT JOIN tasks t ON t.column_id = c.id
			AND t.deleted_at IS NULL
			AND ($2::timestamptz IS NULL OR t.created_at >= $2::timestamptz)
			AND ($3::timestamptz IS NULL OR t.created_at <= $3::timestamptz)
		WHERE c.board_id = $1
		GROUP BY c.id, c.key, c.name, c.position
		ORDER BY c.position ASC, c.name ASC
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.BoardStatsColumnItem{}
	for rows.Next() {
		var item domain.BoardStatsColumnItem
		if err := rows.Scan(&item.ColumnID, &item.Key, &item.Name, &item.Position, &item.Count); err != nil {
			return nil, err
		}
		item.Percent = percent(item.Count, total)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) listBoardStatsByPriority(ctx context.Context, boardID uuid.UUID, fromArg, toArg any, total int) ([]domain.BoardStatsCountItem, error) {
	query := `
		SELECT COALESCE(NULLIF(TRIM(priority), ''), 'none') AS priority, COUNT(*)::int
		FROM tasks
		WHERE board_id = $1
		  AND deleted_at IS NULL
		  AND ($2::timestamptz IS NULL OR created_at >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR created_at <= $3::timestamptz)
		GROUP BY priority
		ORDER BY COUNT(*) DESC, priority ASC
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.BoardStatsCountItem{}
	for rows.Next() {
		var item domain.BoardStatsCountItem
		if err := rows.Scan(&item.Key, &item.Count); err != nil {
			return nil, err
		}
		item.Name = item.Key
		item.Percent = percent(item.Count, total)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) listBoardStatsByDeadline(ctx context.Context, boardID uuid.UUID, fromArg, toArg any, total int) ([]domain.BoardStatsCountItem, error) {
	query := `
		WITH filtered AS (
			SELECT t.*, COALESCE(c.key, '') AS column_key
			FROM tasks t
			LEFT JOIN kanban_columns c ON c.id = t.column_id
			WHERE t.board_id = $1
			  AND t.deleted_at IS NULL
			  AND ($2::timestamptz IS NULL OR t.created_at >= $2::timestamptz)
			  AND ($3::timestamptz IS NULL OR t.created_at <= $3::timestamptz)
		)
		SELECT bucket, COUNT(*)::int
		FROM (
			SELECT CASE
				WHEN due_at IS NULL THEN 'no_due'
				WHEN status = 'ГОТОВО' OR column_key = 'DONE' THEN 'done'
				WHEN due_at < NOW() THEN 'overdue'
				WHEN due_at::date = CURRENT_DATE THEN 'due_today'
				WHEN due_at <= NOW() + INTERVAL '7 days' THEN 'due_week'
				ELSE 'later'
			END AS bucket
			FROM filtered
		) x
		GROUP BY bucket
		ORDER BY CASE bucket
			WHEN 'overdue' THEN 1
			WHEN 'due_today' THEN 2
			WHEN 'due_week' THEN 3
			WHEN 'later' THEN 4
			WHEN 'no_due' THEN 5
			WHEN 'done' THEN 6
			ELSE 7
		END
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := map[string]string{
		"overdue":   "Просрочено",
		"due_today": "Сегодня",
		"due_week":  "В течение 7 дней",
		"later":     "Позже",
		"no_due":    "Без срока",
		"done":      "Завершено",
	}
	items := []domain.BoardStatsCountItem{}
	for rows.Next() {
		var item domain.BoardStatsCountItem
		if err := rows.Scan(&item.Key, &item.Count); err != nil {
			return nil, err
		}
		item.Name = names[item.Key]
		if item.Name == "" {
			item.Name = item.Key
		}
		item.Percent = percent(item.Count, total)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) listBoardStatsEmployees(ctx context.Context, boardID uuid.UUID, fromArg, toArg any) ([]domain.BoardStatsEmployee, error) {
	query := `
		WITH board_users AS (
			SELECT DISTINCT bm.user_id
			FROM board_members bm
			WHERE bm.board_id = $1
			UNION
			SELECT DISTINCT ta.user_id
			FROM task_assignees ta
			JOIN tasks t ON t.id = ta.task_id
			WHERE t.board_id = $1 AND t.deleted_at IS NULL
		),
		filtered_tasks AS (
			SELECT t.*, COALESCE(c.key, '') AS column_key
			FROM tasks t
			LEFT JOIN kanban_columns c ON c.id = t.column_id
			WHERE t.board_id = $1
			  AND t.deleted_at IS NULL
			  AND ($2::timestamptz IS NULL OR t.created_at >= $2::timestamptz)
			  AND ($3::timestamptz IS NULL OR t.created_at <= $3::timestamptz)
		),
		assigned AS (
			SELECT
				ta.user_id,
				COUNT(DISTINCT ft.id)::int AS assigned_tasks,
				COUNT(DISTINCT ft.id) FILTER (WHERE ft.status = 'ГОТОВО' OR ft.column_key = 'DONE')::int AS completed_tasks,
				COUNT(DISTINCT ft.id) FILTER (WHERE NOT (ft.status = 'ГОТОВО' OR ft.column_key = 'DONE'))::int AS active_tasks,
				COUNT(DISTINCT ft.id) FILTER (WHERE ft.due_at IS NOT NULL AND ft.due_at < NOW() AND NOT (ft.status = 'ГОТОВО' OR ft.column_key = 'DONE'))::int AS overdue_tasks,
				COALESCE(SUM(ft.original_estimate_sec), 0)::bigint AS total_estimate_seconds,
				COALESCE(SUM(ft.time_spent_sec), 0)::bigint AS total_spent_seconds
			FROM filtered_tasks ft
			JOIN task_assignees ta ON ta.task_id = ft.id
			GROUP BY ta.user_id
		),
		created AS (
			SELECT created_by AS user_id, COUNT(*)::int AS created_tasks
			FROM filtered_tasks
			GROUP BY created_by
		),
		worklogged AS (
			SELECT wl.user_id, COALESCE(SUM(wl.time_spent_sec), 0)::bigint AS worklog_seconds
			FROM task_worklogs wl
			JOIN tasks t ON t.id = wl.task_id
			WHERE t.board_id = $1
			  AND t.deleted_at IS NULL
			  AND ($2::timestamptz IS NULL OR wl.created_at >= $2::timestamptz)
			  AND ($3::timestamptz IS NULL OR wl.created_at <= $3::timestamptz)
			GROUP BY wl.user_id
		)
		SELECT
			u.id,
			COALESCE(NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.username, u.email, u.id::text) AS full_name,
			COALESCE(a.assigned_tasks, 0)::int,
			COALESCE(c.created_tasks, 0)::int,
			COALESCE(a.completed_tasks, 0)::int,
			COALESCE(a.active_tasks, 0)::int,
			COALESCE(a.overdue_tasks, 0)::int,
			COALESCE(a.total_estimate_seconds, 0)::bigint,
			COALESCE(a.total_spent_seconds, 0)::bigint,
			COALESCE(w.worklog_seconds, 0)::bigint
		FROM board_users bu
		JOIN users u ON u.id = bu.user_id
		LEFT JOIN assigned a ON a.user_id = u.id
		LEFT JOIN created c ON c.user_id = u.id
		LEFT JOIN worklogged w ON w.user_id = u.id
		ORDER BY COALESCE(a.completed_tasks, 0) DESC, COALESCE(a.assigned_tasks, 0) DESC, full_name ASC
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.BoardStatsEmployee{}
	for rows.Next() {
		var item domain.BoardStatsEmployee
		if err := rows.Scan(
			&item.UserID,
			&item.FullName,
			&item.AssignedTasks,
			&item.CreatedTasks,
			&item.CompletedTasks,
			&item.ActiveTasks,
			&item.OverdueTasks,
			&item.TotalEstimateSeconds,
			&item.TotalSpentSeconds,
			&item.WorklogSeconds,
		); err != nil {
			return nil, err
		}
		item.CompletionPercent = percent(item.CompletedTasks, item.AssignedTasks)
		item.OverduePercent = percent(item.OverdueTasks, item.AssignedTasks)
		item.ProductivityScore = employeeProductivityScore(item)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) listBoardStatsActivity(ctx context.Context, boardID uuid.UUID, fromArg, toArg any) ([]domain.BoardStatsActivityItem, error) {
	query := `
		SELECT th.action, COUNT(*)::int
		FROM task_history th
		JOIN tasks t ON t.id = th.task_id
		WHERE t.board_id = $1
		  AND t.deleted_at IS NULL
		  AND ($2::timestamptz IS NULL OR th.created_at >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR th.created_at <= $3::timestamptz)
		GROUP BY th.action
		ORDER BY COUNT(*) DESC, th.action ASC
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.BoardStatsActivityItem{}
	for rows.Next() {
		var item domain.BoardStatsActivityItem
		if err := rows.Scan(&item.Action, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TaskRepository) listBoardStatsWorklogs(ctx context.Context, boardID uuid.UUID, fromArg, toArg any) ([]domain.BoardStatsWorklogDay, error) {
	query := `
		SELECT wl.created_at::date::text AS day, COALESCE(SUM(wl.time_spent_sec), 0)::bigint
		FROM task_worklogs wl
		JOIN tasks t ON t.id = wl.task_id
		WHERE t.board_id = $1
		  AND t.deleted_at IS NULL
		  AND ($2::timestamptz IS NULL OR wl.created_at >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR wl.created_at <= $3::timestamptz)
		GROUP BY day
		ORDER BY day ASC
	`
	rows, err := r.db.Query(ctx, query, boardID, fromArg, toArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.BoardStatsWorklogDay{}
	for rows.Next() {
		var item domain.BoardStatsWorklogDay
		if err := rows.Scan(&item.Date, &item.TotalSeconds); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func percent(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return round2(float64(part) * 100 / float64(total))
}

func employeeProductivityScore(item domain.BoardStatsEmployee) float64 {
	if item.AssignedTasks == 0 {
		return 0
	}

	completionScore := item.CompletionPercent * 0.55
	onTimeScore := (100 - item.OverduePercent) * 0.30
	loggingScore := float64(0)
	if item.TotalSpentSeconds > 0 || item.WorklogSeconds > 0 {
		loggingScore = 15
	}
	return round2(math.Min(100, completionScore+onTimeScore+loggingScore))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

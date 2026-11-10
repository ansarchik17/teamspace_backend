package repository

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func periodBounds(period string) (time.Time, time.Time) {
	now := time.Now().UTC()
	switch period {
	case "day":
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return from, from.Add(24 * time.Hour)
	case "month":
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return from, from.AddDate(0, 1, 0)
	default: // week
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		from := time.Date(now.Year(), now.Month(), now.Day()-wd+1, 0, 0, 0, 0, time.UTC)
		return from, from.AddDate(0, 0, 7)
	}
}

// ── 1. SLA Compliance ────────────────────────────────────────────────────────

func (r *TaskRepository) GetSLACompliance(ctx context.Context, boardIDs []uuid.UUID, boardID string, period string) (*domain.SLAComplianceResponse, error) {
	from, to := periodBounds(period)

	query := `
		WITH done_events AS (
			SELECT th.task_id, th.created_at
			FROM task_history th
			WHERE th.action = 'updated'
			  AND th.field_name = 'status'
			  AND th.new_value = 'ГОТОВО'
			UNION ALL
			SELECT th.task_id, th.created_at
			FROM task_history th
			JOIN kanban_columns c ON c.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND c.key = 'DONE'
		),
		closed AS (
			SELECT t.id,
			       t.due_at,
			       MIN(de.created_at) AS closed_at
			FROM tasks t
			JOIN done_events de ON de.task_id = t.id
			WHERE t.board_id = ANY($1)
			  AND t.deleted_at IS NULL
			  AND de.created_at >= $2
			  AND de.created_at <  $3
			GROUP BY t.id, t.due_at
		)
		SELECT
			COUNT(*)::int                                               AS total_closed,
			COUNT(*) FILTER (WHERE due_at IS NOT NULL AND closed_at <= due_at)::int AS on_time,
			COUNT(*) FILTER (WHERE due_at IS NOT NULL AND closed_at >  due_at)::int AS overdue
		FROM closed
	`

	res := &domain.SLAComplianceResponse{
		BoardID:  boardID,
		Period:   period,
		DateFrom: from.Format(time.RFC3339),
		DateTo:   to.Format(time.RFC3339),
	}
	if err := r.db.QueryRow(ctx, query, boardIDs, from, to).
		Scan(&res.TotalClosed, &res.ClosedOnTime, &res.ClosedOverdue); err != nil {
		return nil, err
	}
	if res.TotalClosed > 0 {
		res.CompliancePercent = round2(float64(res.ClosedOnTime) * 100 / float64(res.TotalClosed))
	}
	return res, nil
}

// ── 2. Cycle Time ────────────────────────────────────────────────────────────

func (r *TaskRepository) GetCycleTime(ctx context.Context, boardIDs []uuid.UUID, boardID string, period string) (*domain.CycleTimeResponse, error) {
	from, to := periodBounds(period)

	query := `
		WITH done_events AS (
			SELECT th.task_id, th.created_at
			FROM task_history th
			WHERE th.action = 'updated'
			  AND th.field_name = 'status'
			  AND th.new_value = 'ГОТОВО'
			UNION ALL
			SELECT th.task_id, th.created_at
			FROM task_history th
			JOIN kanban_columns c ON c.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND c.key = 'DONE'
		),
		completed AS (
			SELECT t.id,
			       t.created_at,
			       MIN(de.created_at) AS done_at
			FROM tasks t
			JOIN done_events de ON de.task_id = t.id
			WHERE t.board_id = ANY($1)
			  AND t.deleted_at IS NULL
			  AND de.created_at >= $2
			  AND de.created_at <  $3
			GROUP BY t.id, t.created_at
		)
		SELECT
			COUNT(*)::int AS sample,
			COALESCE(AVG(EXTRACT(EPOCH FROM (done_at - created_at))/3600), 0)::float8 AS avg_hours,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (done_at - created_at))/3600), 0)::float8 AS median_hours
		FROM completed
	`

	res := &domain.CycleTimeResponse{
		BoardID:  boardID,
		Period:   period,
		DateFrom: from.Format(time.RFC3339),
		DateTo:   to.Format(time.RFC3339),
	}
	if err := r.db.QueryRow(ctx, query, boardIDs, from, to).
		Scan(&res.SampleSize, &res.AvgCycleHours, &res.MedianCycleHours); err != nil {
		return nil, err
	}
	res.AvgCycleHours = round2(res.AvgCycleHours)
	res.AvgCycleDays = round2(res.AvgCycleHours / 24)
	res.MedianCycleHours = round2(res.MedianCycleHours)
	return res, nil
}

// ── 3. Overdue Tasks ─────────────────────────────────────────────────────────

func (r *TaskRepository) GetOverdueTasks(ctx context.Context, boardIDs []uuid.UUID) (*domain.OverdueTasksResponse, error) {
	query := `
		SELECT t.id::text, t.task_key, t.title, t.board_id::text,
		       COALESCE(b.name,'') AS board_name,
		       t.status, t.priority, t.due_at
		FROM tasks t
		LEFT JOIN boards b ON b.id = t.board_id
		LEFT JOIN kanban_columns c ON c.id = t.column_id
		WHERE t.deleted_at IS NULL
		  AND t.board_id = ANY($1)
		  AND t.due_at IS NOT NULL
		  AND t.due_at < NOW()
		  AND NOT (COALESCE(t.status, '') = 'ГОТОВО' OR COALESCE(c.key, '') = 'DONE')
		ORDER BY t.due_at ASC
		LIMIT 200
	`
	rows, err := r.db.Query(ctx, query, boardIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	var tasks []domain.OverdueTask
	for rows.Next() {
		var ot domain.OverdueTask
		if err := rows.Scan(&ot.ID, &ot.TaskKey, &ot.Title, &ot.BoardID, &ot.BoardName,
			&ot.Status, &ot.Priority, &ot.DueAt); err != nil {
			return nil, err
		}
		if ot.DueAt != nil {
			ot.DaysOverdue = int(math.Floor(now.Sub(*ot.DueAt).Hours() / 24))
		}
		ot.AssigneeIDs, _ = r.GetAssignees(ctx, mustParseUUID(ot.ID))
		tasks = append(tasks, ot)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &domain.OverdueTasksResponse{Count: len(tasks), Tasks: tasks}, nil
}

// ── 4. Reopen Rate ───────────────────────────────────────────────────────────

func (r *TaskRepository) GetReopenRate(ctx context.Context, boardIDs []uuid.UUID, boardID string, period string) (*domain.ReopenRateResponse, error) {
	from, to := periodBounds(period)

	query := `
		WITH period_tasks AS (
			SELECT DISTINCT t.id
			FROM tasks t
			WHERE t.deleted_at IS NULL
			  AND t.board_id = ANY($1)
			  AND t.created_at >= $2
			  AND t.created_at <  $3
		),
		reopened AS (
			SELECT DISTINCT th.task_id
			FROM task_history th
			JOIN period_tasks pt ON pt.id = th.task_id
			WHERE th.action = 'updated'
			  AND th.field_name = 'status'
			  AND th.old_value = 'В ПРОЦЕССЕ ПРОВЕРКИ'
			  AND th.new_value = 'В РАБОТЕ'
			UNION
			SELECT DISTINCT th.task_id
			FROM task_history th
			JOIN period_tasks pt ON pt.id = th.task_id
			JOIN kanban_columns old_col ON old_col.id::text = th.old_value
			JOIN kanban_columns new_col ON new_col.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND old_col.key = 'TEST'
			  AND new_col.key = 'IN_PROGRESS'
		)
		SELECT
			(SELECT COUNT(*)::int FROM period_tasks) AS total,
			(SELECT COUNT(*)::int FROM reopened)     AS reopened
	`

	res := &domain.ReopenRateResponse{
		BoardID:  boardID,
		Period:   period,
		DateFrom: from.Format(time.RFC3339),
		DateTo:   to.Format(time.RFC3339),
	}
	var total, reopened int
	if err := r.db.QueryRow(ctx, query, boardIDs, from, to).Scan(&total, &reopened); err != nil {
		return nil, err
	}
	res.TotalTasks = total
	res.ReopenedCount = reopened
	if total > 0 {
		res.ReopenRate = round2(float64(reopened) * 100 / float64(total))
	}
	return res, nil
}

// ── 5. Time in Status ────────────────────────────────────────────────────────

func (r *TaskRepository) GetTimeInStatus(ctx context.Context, boardIDs []uuid.UUID, boardID string, period string) (*domain.TimeInStatusResponse, error) {
	from, to := periodBounds(period)

	// Build intervals between consecutive status changes per task
	query := `
		WITH raw_changes AS (
			SELECT th.task_id, th.new_value AS status, th.created_at
			FROM task_history th
			JOIN tasks t ON t.id = th.task_id
			WHERE th.action    = 'updated'
			  AND th.field_name = 'status'
			  AND t.deleted_at  IS NULL
			  AND t.board_id = ANY($1)
			  AND th.created_at >= $2
			  AND th.created_at <  $3
			UNION ALL
			SELECT th.task_id, c.name AS status, th.created_at
			FROM task_history th
			JOIN tasks t ON t.id = th.task_id
			JOIN kanban_columns c ON c.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND t.deleted_at IS NULL
			  AND t.board_id = ANY($1)
			  AND th.created_at >= $2
			  AND th.created_at < $3
		),
		status_changes AS (
			SELECT task_id,
			       status,
			       created_at AS entered_at,
			       LEAD(created_at) OVER (PARTITION BY task_id ORDER BY created_at) AS exited_at
			FROM raw_changes
		)
		SELECT
			status,
			COUNT(*)::int AS cnt,
			COALESCE(AVG(EXTRACT(EPOCH FROM (COALESCE(exited_at, NOW()) - entered_at))/3600), 0)::float8 AS avg_hours
		FROM status_changes
		WHERE status IS NOT NULL
		GROUP BY status
		ORDER BY avg_hours DESC
	`

	rows, err := r.db.Query(ctx, query, boardIDs, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.TimeInStatusItem
	for rows.Next() {
		var it domain.TimeInStatusItem
		if err := rows.Scan(&it.Status, &it.TaskCount, &it.AvgHours); err != nil {
			return nil, err
		}
		it.AvgHours = round2(it.AvgHours)
		it.AvgDays = round2(it.AvgHours / 24)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &domain.TimeInStatusResponse{
		BoardID:  boardID,
		Period:   period,
		DateFrom: from.Format(time.RFC3339),
		DateTo:   to.Format(time.RFC3339),
		Statuses: items,
	}, nil
}

// ── 6. Workload Distribution ─────────────────────────────────────────────────

func (r *TaskRepository) GetWorkloadDistribution(ctx context.Context, boardIDs []uuid.UUID, boardID string, groupBy string) (*domain.WorkloadDistributionResponse, error) {
	var col string
	join := ""
	switch groupBy {
	case "priority":
		col = "COALESCE(NULLIF(TRIM(t.priority),''),'none')"
	case "status":
		col = "COALESCE(NULLIF(TRIM(t.status),''),'unknown')"
	case "type":
		col = "COALESCE(NULLIF(TRIM(l.name),''),'Без типа')"
		join = `
		LEFT JOIN kanban_task_labels ktl ON ktl.task_id = t.id
		LEFT JOIN kanban_labels l ON l.id = ktl.label_id
		`
	default:
		col = "COALESCE(NULLIF(TRIM(t.priority),''),'none')"
		groupBy = "priority"
	}

	query := `
		SELECT ` + col + ` AS key, COUNT(*)::int
		FROM tasks t
		LEFT JOIN kanban_columns c ON c.id = t.column_id
		` + join + `
		WHERE t.deleted_at IS NULL
		  AND t.board_id = ANY($1)
		  AND NOT (COALESCE(t.status, '') = 'ГОТОВО' OR COALESCE(c.key, '') = 'DONE')
		GROUP BY 1
		ORDER BY COUNT(*) DESC
	`

	rows, err := r.db.Query(ctx, query, boardIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.WorkloadDistributionItem
	total := 0
	for rows.Next() {
		var it domain.WorkloadDistributionItem
		if err := rows.Scan(&it.Key, &it.Count); err != nil {
			return nil, err
		}
		it.Name = it.Key
		total += it.Count
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Percent = percent(items[i].Count, total)
	}

	return &domain.WorkloadDistributionResponse{
		BoardID: boardID,
		GroupBy: groupBy,
		Items:   items,
	}, nil
}

// ── 7. Capacity ──────────────────────────────────────────────────────────────

const defaultWIPLimit = 5

func (r *TaskRepository) GetCapacity(ctx context.Context, boardIDs []uuid.UUID, boardID string) (*domain.CapacityResponse, error) {
	query := `
		SELECT u.id, COALESCE(NULLIF(TRIM(COALESCE(u.first_name,'')||' '||COALESCE(u.last_name,'')),''),(u.username)) AS name,
		       COUNT(DISTINCT t.id) FILTER (WHERE NOT (COALESCE(t.status, '') = 'ГОТОВО' OR COALESCE(c.key, '') = 'DONE'))::int AS active
		FROM task_assignees ta
		JOIN tasks t ON t.id = ta.task_id
		LEFT JOIN kanban_columns c ON c.id = t.column_id
		JOIN users u ON u.id = ta.user_id
		WHERE t.deleted_at IS NULL
		  AND t.board_id = ANY($1)
		GROUP BY 1, 2
		ORDER BY active DESC
	`

	rows, err := r.db.Query(ctx, query, boardIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loads []domain.AssigneeLoad
	totalActive := 0
	for rows.Next() {
		var al domain.AssigneeLoad
		if err := rows.Scan(&al.UserID, &al.FullName, &al.ActiveTasks); err != nil {
			return nil, err
		}
		al.WIPLimit = defaultWIPLimit
		if al.WIPLimit > 0 {
			al.Usage = round2(float64(al.ActiveTasks) * 100 / float64(al.WIPLimit))
		}
		totalActive += al.ActiveTasks
		loads = append(loads, al)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &domain.CapacityResponse{
		BoardID:         boardID,
		WIPLimit:        defaultWIPLimit,
		ActiveTaskCount: totalActive,
		UsagePercent:    round2(float64(totalActive) * 100 / float64(max(len(loads), 1)*defaultWIPLimit)),
		ByAssignee:      loads,
	}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ── 8. Stale Tasks ───────────────────────────────────────────────────────────

func (r *TaskRepository) GetStaleTasks(ctx context.Context, boardIDs []uuid.UUID, thresholdDays int) (*domain.StaleTasksResponse, error) {
	if thresholdDays <= 0 {
		thresholdDays = 7
	}
	cutoff := time.Now().AddDate(0, 0, -thresholdDays)

	query := `
		SELECT t.id::text, t.task_key, t.title, t.board_id::text,
		       COALESCE(b.name,'') AS board_name,
		       t.status, t.priority, t.updated_at, t.due_at
		FROM tasks t
		LEFT JOIN boards b ON b.id = t.board_id
		LEFT JOIN kanban_columns c ON c.id = t.column_id
		WHERE t.deleted_at IS NULL
		  AND t.board_id = ANY($2)
		  AND NOT (COALESCE(t.status, '') = 'ГОТОВО' OR COALESCE(c.key, '') = 'DONE')
		  AND t.updated_at < $1
		ORDER BY t.updated_at ASC
		LIMIT 200
	`

	rows, err := r.db.Query(ctx, query, cutoff, boardIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	var tasks []domain.StaleTask
	taskIDs := make([]uuid.UUID, 0, 200)
	for rows.Next() {
		var st domain.StaleTask
		var lastAct time.Time
		if err := rows.Scan(&st.ID, &st.TaskKey, &st.Title, &st.BoardID, &st.BoardName,
			&st.Status, &st.Priority, &lastAct, &st.DueAt); err != nil {
			return nil, err
		}
		st.LastActivity = &lastAct
		st.DaysSinceAct = int(math.Floor(now.Sub(lastAct).Hours() / 24))
		taskIDs = append(taskIDs, mustParseUUID(st.ID))
		tasks = append(tasks, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	assigneesByTaskID, err := r.getAssigneesByTaskIDs(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		tasks[i].AssigneeIDs = assigneesByTaskID[taskIDs[i]]
	}

	return &domain.StaleTasksResponse{
		ThresholdDays: thresholdDays,
		Count:         len(tasks),
		Tasks:         tasks,
	}, nil
}

func (r *TaskRepository) getAssigneesByTaskIDs(ctx context.Context, taskIDs []uuid.UUID) (map[uuid.UUID][]int64, error) {
	assignees := make(map[uuid.UUID][]int64, len(taskIDs))
	if len(taskIDs) == 0 {
		return assignees, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT task_id, user_id
		FROM task_assignees
		WHERE task_id = ANY($1)
		ORDER BY task_id, user_id
	`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var taskID uuid.UUID
		var userID int64
		if err := rows.Scan(&taskID, &userID); err != nil {
			return nil, err
		}
		assignees[taskID] = append(assignees[taskID], userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return assignees, nil
}

// ── 9. Burn-Down ─────────────────────────────────────────────────────────────

func (r *TaskRepository) GetBurnDown(ctx context.Context, boardID string, from, to time.Time) (*domain.BurnDownResponse, error) {
	query := `
		WITH dates AS (
			SELECT generate_series($2::date, $3::date, '1 day'::interval)::date AS d
		),
		total AS (
			SELECT COUNT(*)::int AS cnt
			FROM tasks
			WHERE board_id = $1
			  AND deleted_at IS NULL
			  AND created_at::date <= $3::date
		),
		done_events AS (
			SELECT th.task_id, th.created_at
			FROM task_history th
			WHERE th.action = 'updated'
			  AND th.field_name = 'status'
			  AND th.new_value = 'ГОТОВО'
			UNION ALL
			SELECT th.task_id, th.created_at
			FROM task_history th
			JOIN kanban_columns c ON c.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND c.key = 'DONE'
		),
		closed_by_day AS (
			SELECT de.created_at::date AS d, COUNT(DISTINCT de.task_id)::int AS done
			FROM done_events de
			JOIN tasks t ON t.id = de.task_id
			WHERE t.board_id = $1
			  AND t.deleted_at IS NULL
			  AND de.created_at::date BETWEEN $2 AND $3
			GROUP BY de.created_at::date
		),
		cumulative AS (
			SELECT d.d,
			       COALESCE(SUM(cbd.done) OVER (ORDER BY d.d), 0)::int AS cum_done
			FROM dates d
			LEFT JOIN closed_by_day cbd ON cbd.d = d.d
		)
		SELECT d::text, (t.cnt - COALESCE(c.cum_done, 0)) AS remaining
		FROM dates d
		JOIN total t ON true
		LEFT JOIN cumulative c ON c.d = d.d
		ORDER BY d.d
	`

	bid, _ := uuid.Parse(boardID)
	rows, err := r.db.Query(ctx, query, bid, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []domain.BurnDownPoint
	for rows.Next() {
		var p domain.BurnDownPoint
		if err := rows.Scan(&p.Date, &p.TotalTasksRemaining); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &domain.BurnDownResponse{
		BoardID:    boardID,
		DateFrom:   from.Format(time.RFC3339),
		DateTo:     to.Format(time.RFC3339),
		DataPoints: points,
	}, nil
}

// ── 10. Throughput ───────────────────────────────────────────────────────────

func (r *TaskRepository) GetThroughput(ctx context.Context, boardIDs []uuid.UUID, boardID string, period string) (*domain.ThroughputResponse, error) {
	from, to := periodBounds(period)

	query := `
		WITH dates AS (
			SELECT generate_series($2::date, ($3::date - interval '1 day')::date, '1 day'::interval)::date AS d
		),
		created AS (
			SELECT created_at::date AS d, COUNT(*)::int AS cnt
			FROM tasks
			WHERE deleted_at IS NULL
			  AND board_id = ANY($1)
			  AND created_at >= $2 AND created_at < $3
			GROUP BY d
		),
		done_events AS (
			SELECT th.task_id, th.created_at
			FROM task_history th
			WHERE th.action = 'updated'
			  AND th.field_name = 'status'
			  AND th.new_value = 'ГОТОВО'
			UNION ALL
			SELECT th.task_id, th.created_at
			FROM task_history th
			JOIN kanban_columns c ON c.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND c.key = 'DONE'
		),
		closed AS (
			SELECT de.created_at::date AS d, COUNT(DISTINCT de.task_id)::int AS cnt
			FROM done_events de
			JOIN tasks t ON t.id = de.task_id
			WHERE t.deleted_at IS NULL
			  AND t.board_id = ANY($1)
			  AND de.created_at >= $2 AND de.created_at < $3
			GROUP BY d
		)
		SELECT dates.d::text,
		       COALESCE(cr.cnt,0) AS created,
		       COALESCE(cl.cnt,0) AS closed
		FROM dates
		LEFT JOIN created cr ON cr.d = dates.d
		LEFT JOIN closed  cl ON cl.d = dates.d
		ORDER BY dates.d
	`

	rows, err := r.db.Query(ctx, query, boardIDs, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []domain.ThroughputPoint
	for rows.Next() {
		var p domain.ThroughputPoint
		if err := rows.Scan(&p.Date, &p.Created, &p.Closed); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &domain.ThroughputResponse{
		BoardID:    boardID,
		DateFrom:   from.Format(time.RFC3339),
		DateTo:     to.Format(time.RFC3339),
		DataPoints: points,
	}, nil
}

// ── 11. Efficiency Trend ─────────────────────────────────────────────────────

func (r *TaskRepository) GetEfficiencyTrend(ctx context.Context, boardIDs []uuid.UUID, boardID string, period string) (*domain.EfficiencyTrendResponse, error) {
	from, to := periodBounds(period)

	query := `
		WITH dates AS (
			SELECT generate_series($2::date, ($3::date - interval '1 day')::date, '1 day'::interval)::date AS d
		),
		done_events AS (
			SELECT th.task_id, th.created_at
			FROM task_history th
			WHERE th.action = 'updated'
			  AND th.field_name = 'status'
			  AND th.new_value = 'ГОТОВО'
			UNION ALL
			SELECT th.task_id, th.created_at
			FROM task_history th
			JOIN kanban_columns c ON c.id::text = th.new_value
			WHERE th.action = 'moved'
			  AND c.key = 'DONE'
		),
		closed_per_day AS (
			SELECT de.created_at::date AS d, COUNT(DISTINCT de.task_id)::int AS completed
			FROM done_events de
			JOIN tasks t ON t.id = de.task_id
			WHERE t.deleted_at IS NULL
			  AND t.board_id = ANY($1)
			  AND de.created_at >= $2 AND de.created_at < $3
			GROUP BY d
		),
		active_per_day AS (
			SELECT t.created_at::date AS d, COUNT(*)::int AS active
			FROM tasks t
			LEFT JOIN kanban_columns c ON c.id = t.column_id
			WHERE t.deleted_at IS NULL
			  AND t.board_id = ANY($1)
			  AND t.created_at >= $2 AND t.created_at < $3
			  AND NOT (COALESCE(t.status, '') = 'ГОТОВО' OR COALESCE(c.key, '') = 'DONE')
			GROUP BY d
		)
		SELECT dates.d::text,
		       COALESCE(cpd.completed,0) AS completed,
		       COALESCE(apd.active,0)    AS active
		FROM dates
		LEFT JOIN closed_per_day cpd ON cpd.d = dates.d
		LEFT JOIN active_per_day apd ON apd.d = dates.d
		ORDER BY dates.d
	`

	rows, err := r.db.Query(ctx, query, boardIDs, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []domain.EfficiencyPoint
	for rows.Next() {
		var p domain.EfficiencyPoint
		if err := rows.Scan(&p.Date, &p.CompletedTasks, &p.TotalActive); err != nil {
			return nil, err
		}
		total := p.CompletedTasks + p.TotalActive
		if total > 0 {
			p.EfficiencyPercent = round2(float64(p.CompletedTasks) * 100 / float64(total))
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &domain.EfficiencyTrendResponse{
		BoardID:    boardID,
		Period:     period,
		DateFrom:   from.Format(time.RFC3339),
		DateTo:     to.Format(time.RFC3339),
		DataPoints: points,
	}, nil
}

// ── util ─────────────────────────────────────────────────────────────────────

func mustParseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

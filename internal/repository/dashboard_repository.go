package repository

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type DashboardRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewDashboardRepository(db *pgxpool.Pool, logger *zap.Logger) *DashboardRepository {
	return &DashboardRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "dashboard")),
	}
}

func (r *DashboardRepository) GetActiveTasksStats(ctx context.Context, userID int64, now time.Time) (int, int, []int, error) {
	// Current tasks assigned for execution (StatusTodo)
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) 
		FROM tasks t
		JOIN task_assignees ta ON t.id = ta.task_id
		JOIN boards b ON t.board_id = b.id
		WHERE ta.user_id = $1 AND t.status = $2 AND t.deleted_at IS NULL
		AND `+kanbanBoardVisibilityCondition("b", "$1")+`
	`, userID, domain.StatusTodo).Scan(&count)
	if err != nil {
		return 0, 0, nil, err
	}

	// Trend percentage: comparing today vs yesterday (at the same time or full day?)
	// Spec says: "compared to the same period yesterday".
	// Let's take count at the end of yesterday vs count now.
	var countYesterday int
	yesterday := now.AddDate(0, 0, -1)
	err = r.db.QueryRow(ctx, `
		SELECT COUNT(*) 
		FROM tasks t
		JOIN task_assignees ta ON t.id = ta.task_id
		JOIN boards b ON t.board_id = b.id
		WHERE ta.user_id = $1 AND t.status = $2 AND t.deleted_at IS NULL
		AND t.created_at <= $3
		AND `+kanbanBoardVisibilityCondition("b", "$1")+`
	`, userID, domain.StatusTodo, yesterday).Scan(&countYesterday)
	if err != nil {
		return count, 0, nil, err
	}

	trend := 0
	if countYesterday > 0 {
		trend = ((count - countYesterday) * 100) / countYesterday
	} else if count > 0 {
		trend = 100
	}

	// Sparkline: last 7 days (count of active tasks at end of each day)
	sparkline := make([]int, 7)
	for i := 0; i < 7; i++ {
		dayEnd := now.AddDate(0, 0, -6+i)
		var dayCount int
		err := r.db.QueryRow(ctx, `
			SELECT COUNT(*) 
			FROM tasks t
			JOIN task_assignees ta ON t.id = ta.task_id
			JOIN boards b ON t.board_id = b.id
			WHERE ta.user_id = $1 AND t.status = $2 AND t.deleted_at IS NULL
			AND t.created_at <= $3
			AND `+kanbanBoardVisibilityCondition("b", "$1")+`
		`, userID, domain.StatusTodo, dayEnd).Scan(&dayCount)
		if err != nil {
			r.logger.Error("failed to get sparkline count", zap.Error(err))
			continue
		}
		sparkline[i] = dayCount
	}

	return count, trend, sparkline, nil
}

func (r *DashboardRepository) GetMeetingsTodayStats(ctx context.Context, userID int64, now time.Time) (int, int, error) {
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.AddDate(0, 0, 1)

	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM events e
		JOIN event_attendees ea ON e.id = ea.event_id
		WHERE ea.user_id = $1 AND e.deleted_at IS NULL
		AND e.start_time >= $2 AND e.start_time < $3
	`, userID, startOfDay, endOfDay).Scan(&count)
	if err != nil {
		return 0, 0, err
	}

	// Trend: today vs yesterday
	var countYesterday int
	startYest := startOfDay.AddDate(0, 0, -1)
	endYest := startOfDay
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM events e
		JOIN event_attendees ea ON e.id = ea.event_id
		WHERE ea.user_id = $1 AND e.deleted_at IS NULL
		AND e.start_time >= $2 AND e.start_time < $3
	`, userID, startYest, endYest).Scan(&countYesterday); err != nil {
		r.logger.Error("failed to get yesterday meetings count", zap.Error(err))
	}

	trend := 0
	if countYesterday > 0 {
		trend = ((count - countYesterday) * 100) / countYesterday
	} else if count > 0 {
		trend = 100
	}

	return count, trend, nil
}

func (r *DashboardRepository) GetDocumentsWeeklyStats(ctx context.Context, userID int64, now time.Time) (int, int, error) {
	// Current week start (Monday)
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	} // Sunday is 7
	startOfWeek := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -weekday+1)

	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM files
		WHERE owner_id = $1 AND deleted_at IS NULL
	`, userID).Scan(&count)
	if err != nil {
		return 0, 0, err
	}

	// Trend: current week vs last week
	var countLastWeek int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM files
		WHERE owner_id = $1 AND deleted_at IS NULL
		AND created_at < $2
	`, userID, startOfWeek).Scan(&countLastWeek); err != nil {
		r.logger.Error("failed to get last week documents count", zap.Error(err))
	}

	trend := 0
	if countLastWeek > 0 {
		trend = ((count - countLastWeek) * 100) / countLastWeek
	} else if count > 0 {
		trend = 100
	}

	return count, trend, nil
}

func (r *DashboardRepository) GetPointsStats(ctx context.Context, userID int64, now time.Time) (int, int, int, error) {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	startOfWeek := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -weekday+1)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var weeklyCount int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(points), 0)
		FROM user_gamification_activities
		WHERE user_id = $1 AND created_at >= $2
	`, userID, startOfWeek).Scan(&weeklyCount)
	if err != nil {
		r.logger.Error("failed to get weekly points", zap.Error(err))
	}

	var todayCount int
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(points), 0)
		FROM user_gamification_activities
		WHERE user_id = $1 AND created_at >= $2
	`, userID, startOfDay).Scan(&todayCount)
	if err != nil {
		r.logger.Error("failed to get today points", zap.Error(err))
	}

	// Trend for points: current week vs last week
	var lastWeeklyCount int
	startLastWeek := startOfWeek.AddDate(0, 0, -7)
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(points), 0)
		FROM user_gamification_activities
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
	`, userID, startLastWeek, startOfWeek).Scan(&lastWeeklyCount)
	if err != nil {
		r.logger.Error("failed to get last week points", zap.Error(err))
	}

	trend := 0
	if lastWeeklyCount > 0 {
		trend = ((weeklyCount - lastWeeklyCount) * 100) / lastWeeklyCount
	} else if weeklyCount > 0 {
		trend = 100
	}

	return weeklyCount, trend, todayCount, nil
}

func (r *DashboardRepository) GetEfficiencyDynamics(ctx context.Context, userID int64, now time.Time) (float64, []int, error) {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	startOfWeek := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -weekday+1)

	data := make([]int, 7)
	total := 0
	for i := 0; i < 7; i++ {
		dStart := startOfWeek.AddDate(0, 0, i)
		dEnd := dStart.AddDate(0, 0, 1)

		var points int
		err := r.db.QueryRow(ctx, `
			SELECT COALESCE(SUM(points), 0)
			FROM user_gamification_activities
			WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		`, userID, dStart, dEnd).Scan(&points)
		if err != nil {
			r.logger.Error("failed to get day load", zap.Error(err))
			continue
		}
		data[i] = points
		total += points
	}

	avg := float64(total) / 7.0
	return avg, data, nil
}

func (r *DashboardRepository) GetTaskStructure(ctx context.Context, userID int64) (int, []domain.DashboardTaskStat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.status, COUNT(*)
		FROM tasks t
		JOIN task_assignees ta ON t.id = ta.task_id
		JOIN boards b ON t.board_id = b.id
		WHERE ta.user_id = $1 AND t.deleted_at IS NULL
		AND `+kanbanBoardVisibilityCondition("b", "$1")+`
		GROUP BY t.status
	`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	statusMap := map[string]int{
		domain.StatusTodo:    0,
		domain.StatusWorking: 0,
		domain.StatusReview:  0,
		domain.StatusDone:    0,
	}
	total := 0

	for rows.Next() {
		var rawStatus string
		var count int
		if err := rows.Scan(&rawStatus, &count); err != nil {
			continue
		}

		// Нормализация статуса для маппинга
		s := strings.ToUpper(strings.TrimSpace(rawStatus))

		// Обработка альтернативных ключей (если в базе лежат ключи колонок)
		if s == "TODO" || s == "К ВЫПОЛНЕНИЮ" {
			s = domain.StatusTodo
		}
		if s == "IN_PROGRESS" || s == "В РАБОТЕ" || s == "WORKING" {
			s = domain.StatusWorking
		}
		if s == "TEST" || s == "REVIEW" || s == "В ПРОЦЕССЕ ПРОВЕРКИ" || s == "ПРОВЕРКА" {
			s = domain.StatusReview
		}
		if s == "DONE" || s == "ГОТОВО" || s == "COMPLETED" {
			s = domain.StatusDone
		}

		statusMap[s] += count
		total += count
	}

	stats := []domain.DashboardTaskStat{
		{Label: "К выполнению", Count: statusMap[domain.StatusTodo]},
		{Label: "В работе", Count: statusMap[domain.StatusWorking]},
		{Label: "Проверка", Count: statusMap[domain.StatusReview]},
		{Label: "Готово", Count: statusMap[domain.StatusDone]},
	}

	return total, stats, nil
}

func (r *DashboardRepository) GetCompletionProgress(ctx context.Context, userID int64) (int, int, int, error) {
	// Для прогресса лучше получить все статусы и суммировать те, что считаются "завершенными"
	rows, err := r.db.Query(ctx, `
		SELECT t.status, COUNT(*)
		FROM tasks t
		JOIN task_assignees ta ON t.id = ta.task_id
		JOIN boards b ON t.board_id = b.id
		WHERE ta.user_id = $1 AND t.deleted_at IS NULL
		AND `+kanbanBoardVisibilityCondition("b", "$1")+`
		GROUP BY t.status
	`, userID)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()

	total := 0
	completed := 0
	for rows.Next() {
		var rawStatus string
		var count int
		if err := rows.Scan(&rawStatus, &count); err != nil {
			continue
		}

		s := strings.ToUpper(strings.TrimSpace(rawStatus))
		if s == "DONE" || s == "ГОТОВО" || s == "COMPLETED" || s == domain.StatusDone {
			completed += count
		}
		total += count
	}

	percentage := 0
	if total > 0 {
		percentage = (completed * 100) / total
	}

	return completed, total, percentage, nil
}

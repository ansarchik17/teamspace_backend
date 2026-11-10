package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type LoginLogRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

type analyticsOrgScope struct {
	MinistryID   *int64
	StateBodyID  *int64
	DepartmentID *int64
	DivisionID   *int64
}

func NewLoginLogRepository(db *pgxpool.Pool, logger *zap.Logger) *LoginLogRepository {
	return &LoginLogRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "login_log")),
	}
}

func (r *LoginLogRepository) Create(ctx context.Context, userID int64, email string) error {
	query := `
		INSERT INTO login_logs (user_id, email, login_time)
		VALUES ($1, $2, $3)
	`
	_, err := r.db.Exec(ctx, query, userID, email, time.Now())
	if err != nil {
		r.logger.Error("Failed to create login log", zap.Error(err), zap.Int64("user_id", userID))
		return fmt.Errorf("create login log: %w", err)
	}
	return nil
}

func (r *LoginLogRepository) TrackSectionVisit(ctx context.Context, userID int64, req domain.SectionVisitTrackRequest) error {
	section, err := domain.NormalizeAnalyticsSection(req.Section)
	if err != nil {
		return apperrors.Validation("invalid analytics section")
	}

	path := domain.NormalizeAnalyticsPath(req.Path)

	query := `
		INSERT INTO user_section_visits (
			user_id, section, route_path, visited_at
		)
		SELECT $1, $2, NULLIF($3, ''), NOW()
		WHERE NOT EXISTS (
			SELECT 1
			FROM user_section_visits
			WHERE user_id = $1
			  AND section = $2
			  AND COALESCE(route_path, '') = COALESCE(NULLIF($3, ''), '')
			  AND visited_at >= NOW() - INTERVAL '30 seconds'
		)
	`

	if _, err := r.db.Exec(
		ctx,
		query,
		userID,
		section,
		path,
	); err != nil {
		r.logger.Error("Failed to track section visit",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("section", section),
			zap.String("path", path),
		)
		return fmt.Errorf("track section visit: %w", err)
	}

	return nil
}

func (r *LoginLogRepository) GetAnalytics(ctx context.Context, req domain.LoginStatsRequest) (*domain.LoginAnalyticsResponse, error) {
	// Base query parts
	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argCounter := 1

	// Filter logic
	if req.StartDate != nil {
		whereClause += fmt.Sprintf(" AND l.login_time >= $%d", argCounter)
		args = append(args, *req.StartDate)
		argCounter++
	} else if req.StartDay != nil {
		// If explicit start "day" string provided (YYYY-MM-DD)
		whereClause += fmt.Sprintf(" AND l.login_time >= $%d::date", argCounter)
		args = append(args, *req.StartDay)
		argCounter++
	}

	if req.EndDate != nil {
		whereClause += fmt.Sprintf(" AND l.login_time <= $%d", argCounter)
		args = append(args, *req.EndDate)
		argCounter++
	} else if req.EndDay != nil {
		// If explicit end "day" string provided (YYYY-MM-DD)
		// To include the end day fully, we usually check < next day or <= end of day.
		// Simple approach: cast login_time to date and check <= provided date
		whereClause += fmt.Sprintf(" AND l.login_time::date <= $%d::date", argCounter)
		args = append(args, *req.EndDay)
		argCounter++
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := req.Offset

	// 1. Get logs with user details
	logsQuery := fmt.Sprintf(`
		SELECT
			l.id, l.user_id, l.email, l.login_time,
			u.username, u.first_name, u.last_name
		FROM login_logs l
		LEFT JOIN users u ON l.user_id = u.id
		%s
		ORDER BY l.login_time DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argCounter, argCounter+1)

	// Add limit/offset to args for the first query
	queryArgs := append(args, limit, offset)

	rows, err := r.db.Query(ctx, logsQuery, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query login logs: %w", err)
	}
	defer rows.Close()

	var logs []*domain.LoginLog
	for rows.Next() {
		log := &domain.LoginLog{}
		if err := rows.Scan(
			&log.ID, &log.UserID, &log.Email, &log.LoginTime,
			&log.UserName, &log.FirstName, &log.LastName,
		); err != nil {
			r.logger.Error("Failed to scan login log", zap.Error(err))
			continue
		}
		logs = append(logs, log)
	}

	// 2. Get Statistics (Total Logins, Unique Users)
	statsQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) as total_logins,
			COUNT(DISTINCT user_id) as unique_users
		FROM login_logs l
		%s
	`, whereClause)

	// Use original args for stats (without limit/offset)
	var totalLogins, uniqueUsers int64
	err = r.db.QueryRow(ctx, statsQuery, args...).Scan(&totalLogins, &uniqueUsers)
	if err != nil {
		r.logger.Error("Failed to get login stats", zap.Error(err))
		// Not critical
	}

	return &domain.LoginAnalyticsResponse{
		TotalLogins: totalLogins,
		UniqueUsers: uniqueUsers,
		Logs:        logs,
	}, nil
}

func (r *LoginLogRepository) GetScopedAnalytics(ctx context.Context, requesterID int64, req domain.LoginStatsRequest) (*domain.LoginAnalyticsResponse, error) {
	var organizationID uuid.NullUUID
	if err := r.db.QueryRow(ctx, `
		SELECT organization_id
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, requesterID).Scan(&organizationID); err != nil {
		return nil, fmt.Errorf("resolve analytics organization: %w", err)
	}

	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argCounter := 1

	if organizationID.Valid {
		whereClause += fmt.Sprintf(" AND u.organization_id = $%d", argCounter)
		args = append(args, organizationID.UUID)
		argCounter++
	}

	whereClause, args, argCounter = appendLoginTimeFilters(whereClause, args, argCounter, req.StartDate, req.EndDate, req.StartDay, req.EndDay, "l.login_time")

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := req.Offset

	logsQuery := fmt.Sprintf(`
		SELECT
			l.id, l.user_id, l.email, l.login_time,
			u.username, u.first_name, u.last_name
		FROM login_logs l
		LEFT JOIN users u ON l.user_id = u.id
		%s
		ORDER BY l.login_time DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argCounter, argCounter+1)

	queryArgs := append(args, limit, offset)
	rows, err := r.db.Query(ctx, logsQuery, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query scoped login logs: %w", err)
	}
	defer rows.Close()

	var logs []*domain.LoginLog
	for rows.Next() {
		log := &domain.LoginLog{}
		if err := rows.Scan(
			&log.ID, &log.UserID, &log.Email, &log.LoginTime,
			&log.UserName, &log.FirstName, &log.LastName,
		); err != nil {
			r.logger.Error("Failed to scan scoped login log", zap.Error(err))
			continue
		}
		logs = append(logs, log)
	}

	statsQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) as total_logins,
			COUNT(DISTINCT l.user_id) as unique_users
		FROM login_logs l
		LEFT JOIN users u ON l.user_id = u.id
		%s
	`, whereClause)

	var totalLogins, uniqueUsers int64
	if err := r.db.QueryRow(ctx, statsQuery, args...).Scan(&totalLogins, &uniqueUsers); err != nil {
		r.logger.Error("Failed to get scoped login stats", zap.Error(err), zap.Int64("requester_id", requesterID))
	}

	return &domain.LoginAnalyticsResponse{
		TotalLogins: totalLogins,
		UniqueUsers: uniqueUsers,
		Logs:        logs,
	}, nil
}

func (r *LoginLogRepository) GetMinistryAnalytics(ctx context.Context, requesterID int64, req domain.MinistryAnalyticsRequest) (*domain.MinistryAnalyticsResponse, error) {
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, apperrors.Validation(err.Error())
	}

	effectiveMinistryID, scope, err := r.resolveAnalyticsScope(ctx, requesterID, req.MinistryID)
	if err != nil {
		return nil, err
	}

	summary, err := r.getMinistryAnalyticsSummary(ctx, effectiveMinistryID, req)
	if err != nil {
		return nil, err
	}

	sectionTotals, err := r.getSectionTotals(ctx, effectiveMinistryID, req)
	if err != nil {
		return nil, err
	}

	ministryRanking, err := r.getMinistryRanking(ctx, effectiveMinistryID, req)
	if err != nil {
		return nil, err
	}

	employeeRanking, err := r.getEmployeeRanking(ctx, effectiveMinistryID, req)
	if err != nil {
		return nil, err
	}

	return &domain.MinistryAnalyticsResponse{
		Scope:               scope,
		EffectiveMinistryID: effectiveMinistryID,
		Summary:             *summary,
		SectionTotals:       sectionTotals,
		MinistryRanking:     ministryRanking,
		EmployeeRanking:     employeeRanking.Items,
		EmployeeTotal:       employeeRanking.Total,
		EmployeeLimit:       req.Limit,
		EmployeeOffset:      req.Offset,
	}, nil
}

type analyticsUserScope struct {
	Role       domain.Role
	MinistryID *int64
}

func (r *LoginLogRepository) getUserOrgScope(ctx context.Context, userID int64) (*analyticsOrgScope, error) {
	scope := &analyticsOrgScope{}
	err := r.db.QueryRow(ctx, `
		SELECT ministry_id, state_body_id, department_id, division_id
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&scope.MinistryID, &scope.StateBodyID, &scope.DepartmentID, &scope.DivisionID)
	if err != nil {
		return nil, fmt.Errorf("get user org scope: %w", err)
	}
	return scope, nil
}

func (r *LoginLogRepository) getAnalyticsUserScope(ctx context.Context, userID int64) (*analyticsUserScope, error) {
	scope := &analyticsUserScope{}
	err := r.db.QueryRow(ctx, `
		SELECT role, ministry_id
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&scope.Role, &scope.MinistryID)
	if err != nil {
		return nil, fmt.Errorf("get analytics user scope: %w", err)
	}
	return scope, nil
}

func (r *LoginLogRepository) resolveAnalyticsScope(ctx context.Context, requesterID int64, requestedMinistryID *int64) (*int64, string, error) {
	scope, err := r.getAnalyticsUserScope(ctx, requesterID)
	if err != nil {
		return nil, "", err
	}

	if domain.HasGlobalUserScope(&domain.User{ID: requesterID, Role: scope.Role, MinistryID: scope.MinistryID}) {
		return requestedMinistryID, "global", nil
	}

	switch scope.Role {
	case domain.RoleManager:
		if scope.MinistryID == nil {
			return nil, "", apperrors.Forbidden("analytics scope is not assigned to this account")
		}
		if requestedMinistryID != nil && *requestedMinistryID != *scope.MinistryID {
			return nil, "", apperrors.Forbidden("requested ministry is outside your analytics scope")
		}
		return scope.MinistryID, "ministry", nil
	default:
		if requestedMinistryID == nil {
			return nil, "", apperrors.Forbidden("ministry_id is required for delegated analytics access")
		}

		allowed, err := r.HasMinistryAnalyticsAccess(ctx, *requestedMinistryID, requesterID)
		if err != nil {
			return nil, "", err
		}
		if allowed {
			return requestedMinistryID, "delegated", nil
		}

		return nil, "", apperrors.Forbidden("analytics is available only for administrators, ministry leadership, or users with delegated access")
	}
}

func (r *LoginLogRepository) HasMinistryAnalyticsAccess(ctx context.Context, ministryID, userID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM ministry_analytics_access
			WHERE ministry_id = $1 AND user_id = $2
		)
	`, ministryID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check ministry analytics access: %w", err)
	}
	return exists, nil
}

func (r *LoginLogRepository) CheckMinistryAnalyticsAccess(ctx context.Context, requesterID, ministryID int64) (*domain.MinistryAnalyticsAccessCheckResponse, error) {
	_, _, err := r.resolveAnalyticsScope(ctx, requesterID, &ministryID)
	if err == nil {
		return &domain.MinistryAnalyticsAccessCheckResponse{
			MinistryID: ministryID,
			Allowed:    true,
		}, nil
	}

	appErr := apperrors.ToAppError(err)
	if appErr.StatusCode == 403 {
		return &domain.MinistryAnalyticsAccessCheckResponse{
			MinistryID: ministryID,
			Allowed:    false,
			Reason:     appErr.Message,
		}, nil
	}

	return nil, err
}

func (r *LoginLogRepository) GetStateBodyUsersAnalytics(ctx context.Context, requesterID int64, req domain.StateBodyUsersAnalyticsRequest) (*domain.StateBodyUsersAnalyticsResponse, error) {
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, apperrors.Validation(err.Error())
	}

	stateBodyID, effectiveMinistryID, stateBodyName, err := r.resolveStateBodyUsersAnalyticsScope(ctx, requesterID, req.StateBodyID)
	if err != nil {
		return nil, err
	}

	summary, err := r.getStateBodyUsersAnalyticsSummary(ctx, req, effectiveMinistryID)
	if err != nil {
		return nil, err
	}

	items, err := r.getStateBodyUsersAnalyticsItems(ctx, req, effectiveMinistryID)
	if err != nil {
		return nil, err
	}

	return &domain.StateBodyUsersAnalyticsResponse{
		StateBodyID:   stateBodyID,
		StateBodyName: stateBodyName,
		Summary:       *summary,
		Items:         items,
		Limit:         req.Limit,
		Offset:        req.Offset,
	}, nil
}

func (r *LoginLogRepository) resolveStateBodyUsersAnalyticsScope(ctx context.Context, requesterID int64, requestedStateBodyID *int64) (*int64, *int64, string, error) {
	return nil, nil, "All users", nil
}

func (r *LoginLogRepository) GrantMinistryAnalyticsAccess(ctx context.Context, performerID, ministryID, targetUserID int64) error {
	if err := r.checkMinistryAnalyticsManageAccess(ctx, performerID, ministryID); err != nil {
		return err
	}
	if err := r.ensureActiveUser(ctx, targetUserID); err != nil {
		return err
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO ministry_analytics_access (ministry_id, user_id, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (ministry_id, user_id) DO UPDATE
		SET granted_by = EXCLUDED.granted_by,
		    created_at = NOW()
	`, ministryID, targetUserID, performerID)
	if err != nil {
		return fmt.Errorf("grant ministry analytics access: %w", err)
	}
	return nil
}

func (r *LoginLogRepository) checkStateBodyAnalyticsAccess(ctx context.Context, requesterID, stateBodyID int64) (string, error) {
	type requesterScope struct {
		Role         domain.Role
		MinistryID   *int64
		StateBodyID  *int64
		DepartmentID *int64
		DivisionID   *int64
	}

	var scope requesterScope
	err := r.db.QueryRow(ctx, `
		SELECT role, ministry_id, state_body_id, department_id, division_id
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, requesterID).Scan(&scope.Role, &scope.MinistryID, &scope.StateBodyID, &scope.DepartmentID, &scope.DivisionID)
	if err != nil {
		return "", fmt.Errorf("get requester analytics scope: %w", err)
	}

	var (
		stateBodyName       string
		stateBodyMinistryID int64
	)
	if err := r.db.QueryRow(ctx, `
		SELECT name, ministry_id
		FROM state_bodies
		WHERE id = $1
	`, stateBodyID).Scan(&stateBodyName, &stateBodyMinistryID); err != nil {
		if err == sql.ErrNoRows {
			return "", apperrors.NotFound("state body")
		}
		return "", fmt.Errorf("get state body for analytics: %w", err)
	}

	switch scope.Role {
	case domain.RoleSuperAdmin:
		return stateBodyName, nil
	}

	var explicitAccess bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM state_body_kanban_stats_access
			WHERE state_body_id = $1 AND user_id = $2
		)
	`, stateBodyID, requesterID).Scan(&explicitAccess); err == nil && explicitAccess {
		return stateBodyName, nil
	}

	if scope.StateBodyID != nil && *scope.StateBodyID == stateBodyID {
		return stateBodyName, nil
	}

	if scope.DepartmentID != nil {
		var departmentStateBodyID int64
		if err := r.db.QueryRow(ctx, `
			SELECT state_body_id
			FROM departments
			WHERE id = $1
		`, *scope.DepartmentID).Scan(&departmentStateBodyID); err == nil && departmentStateBodyID == stateBodyID {
			return stateBodyName, nil
		}
	}

	if scope.DivisionID != nil {
		var divisionStateBodyID sql.NullInt64
		if err := r.db.QueryRow(ctx, `
			SELECT COALESCE(dv.state_body_id, d.state_body_id) AS state_body_id
			FROM divisions dv
			LEFT JOIN departments d ON d.id = dv.department_id
			WHERE dv.id = $1
		`, *scope.DivisionID).Scan(&divisionStateBodyID); err == nil && divisionStateBodyID.Valid && divisionStateBodyID.Int64 == stateBodyID {
			return stateBodyName, nil
		}
	}

	if scope.MinistryID != nil && *scope.MinistryID == stateBodyMinistryID {
		switch scope.Role {
		case domain.RoleManager:
			return stateBodyName, nil
		}
	}

	return "", apperrors.Forbidden("analytics is not available for this state body")
}

func (r *LoginLogRepository) RevokeMinistryAnalyticsAccess(ctx context.Context, performerID, ministryID, targetUserID int64) error {
	if err := r.checkMinistryAnalyticsManageAccess(ctx, performerID, ministryID); err != nil {
		return err
	}

	if _, err := r.db.Exec(ctx, `
		DELETE FROM ministry_analytics_access
		WHERE ministry_id = $1 AND user_id = $2
	`, ministryID, targetUserID); err != nil {
		return fmt.Errorf("revoke ministry analytics access: %w", err)
	}
	return nil
}

func (r *LoginLogRepository) ListMinistryAnalyticsAccess(ctx context.Context, performerID, ministryID int64) ([]domain.MinistryAnalyticsAccessEntry, error) {
	if err := r.checkMinistryAnalyticsManageAccess(ctx, performerID, ministryID); err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT
			a.user_id,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
			a.granted_by,
			a.created_at
		FROM ministry_analytics_access a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.ministry_id = $1
		ORDER BY full_name ASC, a.user_id ASC
	`, ministryID)
	if err != nil {
		return nil, fmt.Errorf("list ministry analytics access: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MinistryAnalyticsAccessEntry, 0)
	for rows.Next() {
		var item domain.MinistryAnalyticsAccessEntry
		if err := rows.Scan(&item.UserID, &item.FullName, &item.GrantedBy, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ministry analytics access: %w", err)
		}
		if item.FullName == "" {
			item.FullName = fmt.Sprintf("User %d", item.UserID)
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func (r *LoginLogRepository) getStateBodyUsersAnalyticsSummary(ctx context.Context, req domain.StateBodyUsersAnalyticsRequest, effectiveMinistryID *int64) (*domain.StateBodyUsersAnalyticsSummary, error) {
	whereUsers, loginJoin, args, argCounter := r.buildStateBodyUsersAnalyticsFilters(req, effectiveMinistryID)

	query := fmt.Sprintf(`
		WITH scoped_users AS (
			SELECT
				u.id,
				u.last_login_at
			FROM users u
			LEFT JOIN departments d ON d.id = u.department_id
			LEFT JOIN divisions dv ON dv.id = u.division_id
			LEFT JOIN departments div_dept ON div_dept.id = dv.department_id
			%s
		),
		scoped_logins AS (
			SELECT l.user_id, l.login_time
			FROM login_logs l
			JOIN scoped_users su ON su.id = l.user_id
			%s
		)
		SELECT
			(SELECT COUNT(*) FROM scoped_users) AS total_users,
			(SELECT COUNT(DISTINCT user_id) FROM scoped_logins) AS logged_users,
			(SELECT COUNT(*) FROM scoped_logins) AS total_logins,
			(SELECT MIN(login_time) FROM scoped_logins) AS period_start,
			(SELECT MAX(login_time) FROM scoped_logins) AS period_end
	`, whereUsers, loginJoin)

	var summary domain.StateBodyUsersAnalyticsSummary
	if err := r.db.QueryRow(ctx, query, args...).Scan(
		&summary.TotalUsers,
		&summary.LoggedUsers,
		&summary.TotalLogins,
		&summary.PeriodStart,
		&summary.PeriodEnd,
	); err != nil {
		return nil, fmt.Errorf("get state body users analytics summary: %w", err)
	}

	_ = argCounter
	return &summary, nil
}

func (r *LoginLogRepository) getStateBodyUsersAnalyticsItems(ctx context.Context, req domain.StateBodyUsersAnalyticsRequest, effectiveMinistryID *int64) ([]domain.StateBodyUserAnalyticsItem, error) {
	whereUsers, loginJoin, args, argCounter := r.buildStateBodyUsersAnalyticsFilters(req, effectiveMinistryID)

	query := fmt.Sprintf(`
		WITH scoped_users AS (
			SELECT
				u.id,
				u.username,
				u.email,
				u.first_name,
				u.last_name,
				COALESCE(hp.job_name, u.position, '') AS position,
				COALESCE(hp.name_go, sb.name, '') AS organization_name,
				d2.name AS department_name,
				dv2.name AS division_name,
				u.last_login_at,
				u.last_seen_at
			FROM users u
			LEFT JOIN user_hr_profiles hp ON hp.user_id = u.id
			LEFT JOIN state_bodies sb ON sb.id = u.state_body_id
			LEFT JOIN departments d ON d.id = u.department_id
			LEFT JOIN divisions dv ON dv.id = u.division_id
			LEFT JOIN departments div_dept ON div_dept.id = dv.department_id
			LEFT JOIN departments d2 ON d2.id = u.department_id
			LEFT JOIN divisions dv2 ON dv2.id = u.division_id
			%s
		),
		paged_users AS (
			SELECT *
			FROM scoped_users
			ORDER BY last_login_at DESC NULLS LAST, id ASC
			LIMIT $%d OFFSET $%d
		),
		login_stats AS (
			SELECT
				l.user_id,
				COUNT(*) AS total_logins,
				MIN(l.login_time) AS first_login_in_period,
				MAX(l.login_time) AS last_login_in_period
			FROM login_logs l
			JOIN paged_users pu ON pu.id = l.user_id
			%s
			GROUP BY l.user_id
		)
		SELECT
			pu.id,
			pu.username,
			pu.email,
			pu.first_name,
			pu.last_name,
			pu.position,
			pu.organization_name,
			pu.department_name,
			pu.division_name,
			pu.last_login_at,
			pu.last_seen_at,
			COALESCE(ls.total_logins, 0) AS total_logins,
			ls.first_login_in_period,
			ls.last_login_in_period
		FROM paged_users pu
		LEFT JOIN login_stats ls ON ls.user_id = pu.id
		ORDER BY pu.last_login_at DESC NULLS LAST, pu.id ASC
	`, whereUsers, argCounter, argCounter+1, loginJoin)

	rows, err := r.db.Query(ctx, query, append(args, req.Limit, req.Offset)...)
	if err != nil {
		return nil, fmt.Errorf("get state body users analytics items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.StateBodyUserAnalyticsItem, 0, req.Limit)
	userIDs := make([]int64, 0, req.Limit)
	for rows.Next() {
		var item domain.StateBodyUserAnalyticsItem
		if err := rows.Scan(
			&item.UserID,
			&item.Username,
			&item.Email,
			&item.FirstName,
			&item.LastName,
			&item.Position,
			&item.OrganizationName,
			&item.DepartmentName,
			&item.DivisionName,
			&item.LastLoginAt,
			&item.LastSeenAt,
			&item.TotalLogins,
			&item.FirstLoginInPeriod,
			&item.LastLoginInPeriod,
		); err != nil {
			return nil, fmt.Errorf("scan state body users analytics item: %w", err)
		}
		item.FullName = strings.TrimSpace(item.FirstName + " " + item.LastName)
		item.LoginEvents = make([]time.Time, 0)
		items = append(items, item)
		userIDs = append(userIDs, item.UserID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return items, nil
	}

	eventsByUser, err := r.getStateBodyUserLoginEvents(ctx, userIDs, req)
	if err != nil {
		return nil, err
	}

	for i := range items {
		items[i].LoginEvents = eventsByUser[items[i].UserID]
	}

	return items, nil
}

func (r *LoginLogRepository) getStateBodyUserLoginEvents(ctx context.Context, userIDs []int64, req domain.StateBodyUsersAnalyticsRequest) (map[int64][]time.Time, error) {
	whereClause := "WHERE l.user_id = ANY($1)"
	args := []interface{}{userIDs}
	argCounter := 2

	whereClause, args, _ = appendLoginTimeFilters(whereClause, args, argCounter, req.StartDate, req.EndDate, req.StartDay, req.EndDay, "l.login_time")

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT l.user_id, l.login_time
		FROM login_logs l
		%s
		ORDER BY l.user_id ASC, l.login_time DESC
	`, whereClause), args...)
	if err != nil {
		return nil, fmt.Errorf("get state body user login events: %w", err)
	}
	defer rows.Close()

	eventsByUser := make(map[int64][]time.Time, len(userIDs))
	for rows.Next() {
		var (
			userID    int64
			loginTime time.Time
		)
		if err := rows.Scan(&userID, &loginTime); err != nil {
			return nil, fmt.Errorf("scan state body user login event: %w", err)
		}
		eventsByUser[userID] = append(eventsByUser[userID], loginTime)
	}

	return eventsByUser, rows.Err()
}

func (r *LoginLogRepository) buildStateBodyUsersAnalyticsFilters(req domain.StateBodyUsersAnalyticsRequest, effectiveMinistryID *int64) (string, string, []interface{}, int) {
	whereUsers := `
			WHERE u.deleted_at IS NULL
			  AND u.is_active = TRUE`
	args := []interface{}{}
	argCounter := 1

	if req.StateBodyID != nil {
		whereUsers += `
			  AND (
				u.state_body_id = $1
				OR d.state_body_id = $1
				OR dv.state_body_id = $1
				OR div_dept.state_body_id = $1
			  )`
		args = append(args, *req.StateBodyID)
		argCounter = 2
	} else if effectiveMinistryID != nil {
		whereUsers += `
			  AND u.ministry_id = $1`
		args = append(args, *effectiveMinistryID)
		argCounter = 2
	}

	if req.Query != "" {
		whereUsers += fmt.Sprintf(`
			  AND (
				u.username ILIKE $%d
				OR u.email ILIKE $%d
				OR u.first_name ILIKE $%d
				OR u.last_name ILIKE $%d
				OR u.full_name_local ILIKE $%d
			  )`, argCounter, argCounter, argCounter, argCounter, argCounter)
		args = append(args, "%"+req.Query+"%")
		argCounter++
	}

	loginJoin := "WHERE 1=1"
	loginJoin, args, argCounter = appendLoginTimeFilters(loginJoin, args, argCounter, req.StartDate, req.EndDate, req.StartDay, req.EndDay, "l.login_time")

	if req.OnlyLoggedIn != nil && *req.OnlyLoggedIn {
		whereUsers += fmt.Sprintf(`
			  AND EXISTS (
				SELECT 1
				FROM login_logs l
				WHERE l.user_id = u.id
				%s
			  )`, strings.TrimPrefix(loginJoin, "WHERE 1=1"))
	}

	return whereUsers, loginJoin, args, argCounter
}

func (r *LoginLogRepository) checkMinistryAnalyticsManageAccess(ctx context.Context, performerID, ministryID int64) error {
	scope, err := r.getAnalyticsUserScope(ctx, performerID)
	if err != nil {
		return err
	}

	if domain.HasGlobalUserScope(&domain.User{ID: performerID, Role: scope.Role, MinistryID: scope.MinistryID}) {
		return nil
	}

	switch scope.Role {
	case domain.RoleManager:
		if scope.MinistryID == nil || *scope.MinistryID != ministryID {
			return apperrors.Forbidden("you cannot manage delegated analytics access outside your ministry")
		}
		return nil
	default:
		return apperrors.Forbidden("you do not have permission to manage delegated analytics access")
	}
}

func (r *LoginLogRepository) ensureActiveUser(ctx context.Context, userID int64) error {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM users
			WHERE id = $1 AND deleted_at IS NULL
		)
	`, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check user existence: %w", err)
	}
	if !exists {
		return apperrors.NotFound("user")
	}
	return nil
}

func (r *LoginLogRepository) getMinistryAnalyticsSummary(ctx context.Context, effectiveMinistryID *int64, req domain.MinistryAnalyticsRequest) (*domain.MinistryAnalyticsSummary, error) {
	type summaryRow struct {
		totalLogins        int64
		uniqueUsers        int64
		totalSectionVisits int64
	}

	loginWhere := "WHERE 1=1"
	sectionWhere := "WHERE 1=1"
	args := []interface{}{}
	argCounter := 1

	if effectiveMinistryID != nil {
		loginWhere += fmt.Sprintf(" AND u.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++

		sectionWhere += fmt.Sprintf(" AND s.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++
	}

	loginWhere, args, argCounter = appendUserOrgFilters(loginWhere, args, argCounter, req, "u", "u")
	sectionWhere, args, argCounter = appendVisitOrgFilters(sectionWhere, args, argCounter, req, "s")
	loginWhere, args, argCounter = appendAnalyticsTimeFilters(loginWhere, args, argCounter, req, "l.login_time")
	sectionWhere, args, _ = appendAnalyticsTimeFilters(sectionWhere, args, argCounter, req, "s.visited_at")

	query := fmt.Sprintf(`
		WITH login_scope AS (
			SELECT l.user_id, COUNT(*) AS total_logins
			FROM login_logs l
			JOIN users u ON u.id = l.user_id
			%s
			GROUP BY l.user_id
		),
		section_scope AS (
			SELECT s.user_id, COUNT(*) AS total_section_visits
			FROM user_section_visits s
			%s
			GROUP BY s.user_id
		),
		combined_users AS (
			SELECT user_id FROM login_scope
			UNION
			SELECT user_id FROM section_scope
		)
		SELECT
			COALESCE((SELECT SUM(total_logins) FROM login_scope), 0) AS total_logins,
			COALESCE((SELECT COUNT(*) FROM combined_users), 0) AS unique_users,
			COALESCE((SELECT SUM(total_section_visits) FROM section_scope), 0) AS total_section_visits
	`, loginWhere, sectionWhere)

	var row summaryRow
	if err := r.db.QueryRow(ctx, query, args...).Scan(&row.totalLogins, &row.uniqueUsers, &row.totalSectionVisits); err != nil {
		return nil, fmt.Errorf("get ministry analytics summary: %w", err)
	}

	return &domain.MinistryAnalyticsSummary{
		TotalLogins:        row.totalLogins,
		UniqueUsers:        row.uniqueUsers,
		TotalSectionVisits: row.totalSectionVisits,
	}, nil
}

func (r *LoginLogRepository) getSectionTotals(ctx context.Context, effectiveMinistryID *int64, req domain.MinistryAnalyticsRequest) ([]domain.AnalyticsSectionTotal, error) {
	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argCounter := 1

	if effectiveMinistryID != nil {
		whereClause += fmt.Sprintf(" AND ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++
	}

	whereClause, args, argCounter = appendVisitOrgFilters(whereClause, args, argCounter, req, "")
	whereClause, args, _ = appendAnalyticsTimeFilters(whereClause, args, argCounter, req, "visited_at")

	query := fmt.Sprintf(`
		SELECT section, COUNT(*) AS visits, COUNT(DISTINCT user_id) AS unique_users
		FROM user_section_visits
		%s
		GROUP BY section
		ORDER BY visits DESC, section ASC
	`, whereClause)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get section totals: %w", err)
	}
	defer rows.Close()

	result := make([]domain.AnalyticsSectionTotal, 0)
	for rows.Next() {
		var item domain.AnalyticsSectionTotal
		if err := rows.Scan(&item.Section, &item.Visits, &item.UniqueUsers); err != nil {
			return nil, fmt.Errorf("scan section totals: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *LoginLogRepository) getMinistryRanking(ctx context.Context, effectiveMinistryID *int64, req domain.MinistryAnalyticsRequest) ([]domain.MinistryRatingItem, error) {
	loginWhere := "WHERE u.deleted_at IS NULL"
	sectionWhere := "WHERE 1=1"
	ministryWhere := ""
	args := []interface{}{}
	argCounter := 1

	if effectiveMinistryID != nil {
		loginWhere += fmt.Sprintf(" AND u.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++

		sectionWhere += fmt.Sprintf(" AND s.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++

		ministryWhere = fmt.Sprintf("WHERE m.id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++
	}

	loginWhere, args, argCounter = appendUserOrgFilters(loginWhere, args, argCounter, req, "u", "u")
	sectionWhere, args, argCounter = appendVisitOrgFilters(sectionWhere, args, argCounter, req, "s")
	ministryWhere, args, argCounter = appendMinistryLevelOrgFilters(ministryWhere, args, argCounter, req, "m")
	loginWhere, args, argCounter = appendAnalyticsTimeFilters(loginWhere, args, argCounter, req, "l.login_time")
	sectionWhere, args, _ = appendAnalyticsTimeFilters(sectionWhere, args, argCounter, req, "s.visited_at")

	query := fmt.Sprintf(`
		WITH active_users AS (
			SELECT ministry_id, COUNT(*) AS active_users
			FROM users
			WHERE deleted_at IS NULL AND is_active = TRUE AND ministry_id IS NOT NULL
			GROUP BY ministry_id
		),
		login_stats AS (
			SELECT u.ministry_id, COUNT(*) AS total_logins
			FROM login_logs l
			JOIN users u ON u.id = l.user_id
			%s
			GROUP BY u.ministry_id
		),
		section_stats AS (
			SELECT s.ministry_id, COUNT(*) AS total_section_visits, COUNT(DISTINCT s.section) AS distinct_sections
			FROM user_section_visits s
			%s
			GROUP BY s.ministry_id
		)
		SELECT
			m.id,
			m.name,
			COALESCE(au.active_users, 0) AS active_users,
			COALESCE(ls.total_logins, 0) AS total_logins,
			COALESCE(ss.total_section_visits, 0) AS total_section_visits,
			COALESCE(ss.distinct_sections, 0) AS distinct_sections,
			(COALESCE(ls.total_logins, 0) * 5) + COALESCE(ss.total_section_visits, 0) + (COALESCE(ss.distinct_sections, 0) * 3) AS activity_score
		FROM ministries m
		LEFT JOIN active_users au ON au.ministry_id = m.id
		LEFT JOIN login_stats ls ON ls.ministry_id = m.id
		LEFT JOIN section_stats ss ON ss.ministry_id = m.id
		%s
		ORDER BY activity_score DESC, total_section_visits DESC, total_logins DESC, m.name ASC
		LIMIT %d
	`, loginWhere, sectionWhere, ministryWhere, req.Limit)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get ministry ranking: %w", err)
	}
	defer rows.Close()

	result := make([]domain.MinistryRatingItem, 0)
	for rows.Next() {
		var item domain.MinistryRatingItem
		if err := rows.Scan(
			&item.MinistryID,
			&item.MinistryName,
			&item.ActiveUsers,
			&item.TotalLogins,
			&item.TotalSectionVisits,
			&item.DistinctSections,
			&item.ActivityScore,
		); err != nil {
			return nil, fmt.Errorf("scan ministry ranking: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type employeeRankingResult struct {
	Items []domain.EmployeeAnalyticsItem
	Total int64
}

func (r *LoginLogRepository) getEmployeeRanking(ctx context.Context, effectiveMinistryID *int64, req domain.MinistryAnalyticsRequest) (*employeeRankingResult, error) {
	loginWhere := "WHERE u.deleted_at IS NULL AND u.is_active = TRUE"
	sectionWhere := "WHERE 1=1"
	userWhere := "WHERE u.deleted_at IS NULL AND u.is_active = TRUE"
	args := []interface{}{}
	argCounter := 1

	countWhere := "WHERE u.deleted_at IS NULL AND u.is_active = TRUE"
	countArgs := []interface{}{}
	countArgCounter := 1

	if effectiveMinistryID != nil {
		loginWhere += fmt.Sprintf(" AND u.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++

		sectionWhere += fmt.Sprintf(" AND s.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++

		userWhere += fmt.Sprintf(" AND u.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++

		countWhere += fmt.Sprintf(" AND u.ministry_id = $%d", countArgCounter)
		countArgs = append(countArgs, *effectiveMinistryID)
		countArgCounter++
	}

	loginWhere, args, argCounter = appendUserOrgFilters(loginWhere, args, argCounter, req, "u", "u")
	sectionWhere, args, argCounter = appendVisitOrgFilters(sectionWhere, args, argCounter, req, "s")
	userWhere, args, argCounter = appendUserOrgFilters(userWhere, args, argCounter, req, "u", "u")
	loginWhere, args, argCounter = appendAnalyticsTimeFilters(loginWhere, args, argCounter, req, "l.login_time")
	sectionWhere, args, sectionArgCounter := appendAnalyticsTimeFilters(sectionWhere, args, argCounter, req, "s.visited_at")
	countWhere, countArgs, _ = appendUserOrgFilters(countWhere, countArgs, countArgCounter, req, "u", "u")

	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM users u
		%s
	`, countWhere)

	var total int64
	if err := r.db.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count employee ranking: %w", err)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`
		WITH login_stats AS (
			SELECT l.user_id, COUNT(*) AS total_logins
			FROM login_logs l
			JOIN users u ON u.id = l.user_id
			%s
			GROUP BY l.user_id
		),
		section_stats AS (
			SELECT s.user_id, COUNT(*) AS total_section_visits, COUNT(DISTINCT s.section) AS distinct_sections
			FROM user_section_visits s
			%s
			GROUP BY s.user_id
		)
		SELECT
			u.id,
			TRIM(CONCAT(u.first_name, ' ', u.last_name)) AS full_name,
			u.position,
			u.ministry_id,
			m.name,
			u.state_body_id,
			sb.name,
			u.department_id,
			d.name,
			u.division_id,
			dv.name,
			COALESCE(ls.total_logins, 0) AS total_logins,
			COALESCE(ss.total_section_visits, 0) AS total_section_visits,
			COALESCE(ss.distinct_sections, 0) AS distinct_sections,
			(COALESCE(ls.total_logins, 0) * 5) + COALESCE(ss.total_section_visits, 0) + (COALESCE(ss.distinct_sections, 0) * 3) AS activity_score,
			u.last_login_at,
			u.last_seen_at
		FROM users u
		LEFT JOIN ministries m ON m.id = u.ministry_id
		LEFT JOIN state_bodies sb ON sb.id = u.state_body_id
		LEFT JOIN departments d ON d.id = u.department_id
		LEFT JOIN divisions dv ON dv.id = u.division_id
		LEFT JOIN login_stats ls ON ls.user_id = u.id
		LEFT JOIN section_stats ss ON ss.user_id = u.id
		%s
		ORDER BY activity_score DESC, total_section_visits DESC, total_logins DESC, u.last_seen_at DESC NULLS LAST, u.id ASC
		LIMIT $%d OFFSET $%d
	`, loginWhere, sectionWhere, userWhere, sectionArgCounter, sectionArgCounter+1)

	queryArgs := append(args, limit, offset)
	rows, err := r.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("get employee ranking: %w", err)
	}
	defer rows.Close()

	result := make([]domain.EmployeeAnalyticsItem, 0)
	for rows.Next() {
		var item domain.EmployeeAnalyticsItem
		if err := rows.Scan(
			&item.UserID,
			&item.FullName,
			&item.Position,
			&item.MinistryID,
			&item.MinistryName,
			&item.StateBodyID,
			&item.StateBodyName,
			&item.DepartmentID,
			&item.DepartmentName,
			&item.DivisionID,
			&item.DivisionName,
			&item.TotalLogins,
			&item.TotalSectionVisits,
			&item.DistinctSections,
			&item.ActivityScore,
			&item.LastLoginAt,
			&item.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scan employee ranking: %w", err)
		}
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.attachEmployeeVisitedSections(ctx, result, effectiveMinistryID, req); err != nil {
		return nil, err
	}

	return &employeeRankingResult{
		Items: result,
		Total: total,
	}, nil
}

func (r *LoginLogRepository) attachEmployeeVisitedSections(ctx context.Context, items []domain.EmployeeAnalyticsItem, effectiveMinistryID *int64, req domain.MinistryAnalyticsRequest) error {
	if len(items) == 0 {
		return nil
	}

	userIDs := make([]int64, 0, len(items))
	indexByUserID := make(map[int64]int, len(items))
	for i := range items {
		userIDs = append(userIDs, items[i].UserID)
		indexByUserID[items[i].UserID] = i
	}

	whereClause := "WHERE s.user_id = ANY($1)"
	args := []interface{}{userIDs}
	argCounter := 2

	if effectiveMinistryID != nil {
		whereClause += fmt.Sprintf(" AND s.ministry_id = $%d", argCounter)
		args = append(args, *effectiveMinistryID)
		argCounter++
	}

	whereClause, args, argCounter = appendVisitOrgFilters(whereClause, args, argCounter, req, "s")
	whereClause, args, _ = appendAnalyticsTimeFilters(whereClause, args, argCounter, req, "s.visited_at")

	query := fmt.Sprintf(`
		SELECT
			s.user_id,
			s.section,
			COALESCE(NULLIF(s.route_path, ''), '') AS route_path,
			COUNT(*) AS visits,
			MAX(s.visited_at) AS last_visited_at
		FROM user_section_visits s
		%s
		GROUP BY s.user_id, s.section, COALESCE(NULLIF(s.route_path, ''), '')
		ORDER BY s.user_id ASC, visits DESC, last_visited_at DESC, s.section ASC, route_path ASC
	`, whereClause)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("get employee visited sections: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			userID      int64
			sectionItem domain.EmployeeVisitedSection
			routePath   string
		)

		if err := rows.Scan(
			&userID,
			&sectionItem.Section,
			&routePath,
			&sectionItem.Visits,
			&sectionItem.LastVisitedAt,
		); err != nil {
			return fmt.Errorf("scan employee visited sections: %w", err)
		}

		if routePath != "" {
			sectionItem.RoutePath = routePath
		}

		if idx, ok := indexByUserID[userID]; ok {
			items[idx].VisitedSections = append(items[idx].VisitedSections, sectionItem)
		}
	}

	return rows.Err()
}

func appendAnalyticsTimeFilters(whereClause string, args []interface{}, argCounter int, req domain.MinistryAnalyticsRequest, column string) (string, []interface{}, int) {
	return appendLoginTimeFilters(whereClause, args, argCounter, req.StartDate, req.EndDate, req.StartDay, req.EndDay, column)
}

func appendUserOrgFilters(whereClause string, args []interface{}, argCounter int, req domain.MinistryAnalyticsRequest, userAlias string, _ string) (string, []interface{}, int) {
	prefix := userAlias
	if prefix != "" {
		prefix += "."
	}
	if req.StateBodyID != nil {
		whereClause += fmt.Sprintf(" AND %sstate_body_id = $%d", prefix, argCounter)
		args = append(args, *req.StateBodyID)
		argCounter++
	}
	if req.DepartmentID != nil {
		whereClause += fmt.Sprintf(" AND %sdepartment_id = $%d", prefix, argCounter)
		args = append(args, *req.DepartmentID)
		argCounter++
	}
	if req.DivisionID != nil {
		whereClause += fmt.Sprintf(" AND %sdivision_id = $%d", prefix, argCounter)
		args = append(args, *req.DivisionID)
		argCounter++
	}
	return whereClause, args, argCounter
}

func appendVisitOrgFilters(whereClause string, args []interface{}, argCounter int, req domain.MinistryAnalyticsRequest, visitAlias string) (string, []interface{}, int) {
	prefix := visitAlias
	if prefix != "" {
		prefix += "."
	}
	if req.StateBodyID != nil {
		whereClause += fmt.Sprintf(" AND %sstate_body_id = $%d", prefix, argCounter)
		args = append(args, *req.StateBodyID)
		argCounter++
	}
	if req.DepartmentID != nil {
		whereClause += fmt.Sprintf(" AND %sdepartment_id = $%d", prefix, argCounter)
		args = append(args, *req.DepartmentID)
		argCounter++
	}
	if req.DivisionID != nil {
		whereClause += fmt.Sprintf(" AND %sdivision_id = $%d", prefix, argCounter)
		args = append(args, *req.DivisionID)
		argCounter++
	}
	return whereClause, args, argCounter
}

func appendMinistryLevelOrgFilters(whereClause string, args []interface{}, argCounter int, req domain.MinistryAnalyticsRequest, ministryAlias string) (string, []interface{}, int) {
	if req.StateBodyID == nil && req.DepartmentID == nil && req.DivisionID == nil {
		return whereClause, args, argCounter
	}

	prefix := ministryAlias
	if prefix != "" {
		prefix += "."
	}

	if strings.TrimSpace(whereClause) == "" {
		whereClause = "WHERE 1=1"
	}

	if req.StateBodyID != nil {
		whereClause += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM state_bodies sb WHERE sb.id = $%d AND sb.ministry_id = %sid)", argCounter, prefix)
		args = append(args, *req.StateBodyID)
		argCounter++
	}
	if req.DepartmentID != nil {
		whereClause += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM departments d JOIN state_bodies sb ON sb.id = d.state_body_id WHERE d.id = $%d AND sb.ministry_id = %sid)", argCounter, prefix)
		args = append(args, *req.DepartmentID)
		argCounter++
	}
	if req.DivisionID != nil {
		whereClause += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM divisions dv LEFT JOIN departments d ON d.id = dv.department_id LEFT JOIN state_bodies sb ON sb.id = COALESCE(d.state_body_id, dv.state_body_id) WHERE dv.id = $%d AND sb.ministry_id = %sid)", argCounter, prefix)
		args = append(args, *req.DivisionID)
		argCounter++
	}

	return whereClause, args, argCounter
}

func appendLoginTimeFilters(whereClause string, args []interface{}, argCounter int, startDate, endDate *time.Time, startDay, endDay *string, column string) (string, []interface{}, int) {
	if startDate != nil {
		whereClause += fmt.Sprintf(" AND %s >= $%d", column, argCounter)
		args = append(args, *startDate)
		argCounter++
	} else if startDay != nil {
		whereClause += fmt.Sprintf(" AND %s >= $%d::date", column, argCounter)
		args = append(args, strings.TrimSpace(*startDay))
		argCounter++
	}

	if endDate != nil {
		whereClause += fmt.Sprintf(" AND %s <= $%d", column, argCounter)
		args = append(args, *endDate)
		argCounter++
	} else if endDay != nil {
		whereClause += fmt.Sprintf(" AND %s < ($%d::date + INTERVAL '1 day')", column, argCounter)
		args = append(args, strings.TrimSpace(*endDay))
		argCounter++
	}

	return whereClause, args, argCounter
}

package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type GamificationRepository struct {
	db *pgxpool.Pool
}

func NewGamificationRepository(db *pgxpool.Pool) *GamificationRepository {
	return &GamificationRepository{db: db}
}

func (r *GamificationRepository) AddActivity(ctx context.Context, userID int64, actionType string, points int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Insert activity
	_, err = tx.Exec(ctx, `
		INSERT INTO user_gamification_activities (user_id, action_type, points)
		VALUES ($1, $2, $3)`, userID, actionType, points)
	if err != nil {
		return err
	}

	// 2. Upsert score table
	_, err = tx.Exec(ctx, `
		INSERT INTO user_gamification_scores (user_id, total_score, level)
		VALUES ($1, $2, 1)
		ON CONFLICT (user_id) DO UPDATE 
		SET total_score = user_gamification_scores.total_score + $2,
		    updated_at = CURRENT_TIMESTAMP`, userID, points)
	if err != nil {
		return err
	}

	// 3. Update level (every 50 points = +1 level)
	_, err = tx.Exec(ctx, `
		UPDATE user_gamification_scores 
		SET level = (total_score / 50) + 1
		WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// HasActivityToday returns true if the user already has a record for the given
// actionType on the current calendar day (UTC). It is used to implement
// "award at most once per day" logic without adding a unique constraint.
func (r *GamificationRepository) HasActivityToday(ctx context.Context, userID int64, actionType string) (bool, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM user_gamification_activities
		WHERE user_id    = $1
		  AND action_type = $2
		  AND created_at >= CURRENT_DATE
		  AND created_at <  CURRENT_DATE + INTERVAL '1 day'`,
		userID, actionType,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *GamificationRepository) GetDashboard(ctx context.Context, limit int) ([]domain.UserGamificationDashboard, error) {
	var dashboard []domain.UserGamificationDashboard
	query := `
		SELECT 
		    u.id AS user_id, 
		    (u.first_name || ' ' || u.last_name) AS full_name,
		    u.position,
		    u.avatar_url,
		    COALESCE(ugs.total_score, 0) AS total_score,
		    COALESCE(ugs.level, 1) AS level
		FROM users u
		LEFT JOIN user_gamification_scores ugs ON u.id = ugs.user_id
		WHERE u.is_active = true
		ORDER BY total_score DESC, u.last_seen_at DESC NULLS LAST
		LIMIT $1
	`
	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var d domain.UserGamificationDashboard
		if err := rows.Scan(&d.UserID, &d.FullName, &d.Position, &d.AvatarURL, &d.TotalScore, &d.Level); err != nil {
			return nil, err
		}
		dashboard = append(dashboard, d)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return dashboard, nil
}

func (r *GamificationRepository) GetMyStats(ctx context.Context, userID int64, startDate, endDate string) (*domain.UserGamificationDashboard, error) {
	// 1. Get total score and level (all-time)
	userQuery := `
		SELECT 
		    u.id AS user_id, 
		    (u.first_name || ' ' || u.last_name) AS full_name,
		    u.position,
		    u.avatar_url,
		    COALESCE(ugs.total_score, 0) AS total_score,
		    COALESCE(ugs.level, 1) AS level
		FROM users u
		LEFT JOIN user_gamification_scores ugs ON u.id = ugs.user_id
		WHERE u.id = $1
	`
	var d domain.UserGamificationDashboard
	err := r.db.QueryRow(ctx, userQuery, userID).Scan(&d.UserID, &d.FullName, &d.Position, &d.AvatarURL, &d.TotalScore, &d.Level)
	if err != nil {
		return nil, err
	}

	// 2. If dates are provided, calculate period score
	if startDate != "" || endDate != "" {
		periodQuery := `SELECT COALESCE(SUM(points), 0) FROM user_gamification_activities WHERE user_id = $1`
		args := []interface{}{userID}
		argCounter := 2

		if startDate != "" {
			periodQuery += ` AND created_at >= $` + string(rune('0'+argCounter)) + `::date`
			args = append(args, startDate)
			argCounter++
		}
		if endDate != "" {
			periodQuery += ` AND created_at <= $` + string(rune('0'+argCounter)) + `::date + interval '1 day'`
			args = append(args, endDate)
		}

		var periodPoints int
		err := r.db.QueryRow(ctx, periodQuery, args...).Scan(&periodPoints)
		if err == nil {
			d.PeriodScore = &periodPoints
		}
	}

	return &d, nil
}

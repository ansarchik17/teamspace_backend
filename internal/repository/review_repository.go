package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ReviewRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewReviewRepository(db *pgxpool.Pool, logger *zap.Logger) *ReviewRepository {
	return &ReviewRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "review")),
	}
}

// CreateReview saves a new review
func (r *ReviewRepository) Create(ctx context.Context, review *domain.UserReview) error {
	query := `
		INSERT INTO user_reviews (
			id, reviewer_id, target_user_id, 
			data_fluency, project_management, digital_proficiency, 
			crisis_communication, service_design_thinking, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.db.Exec(ctx, query,
		review.ID, review.ReviewerID, review.TargetUserID,
		review.DataFluency, review.ProjectManagement, review.DigitalProficiency,
		review.CrisisCommunication, review.ServiceDesign, review.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create review: %w", err)
	}
	return nil
}

// HasReviewedThisMonth checks if reviewer already rated target in current month
func (r *ReviewRepository) HasReviewedThisMonth(ctx context.Context, reviewerID, targetID int64) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM user_reviews 
			WHERE reviewer_id = $1 
			  AND target_user_id = $2 
			  AND date_trunc('month', created_at) = date_trunc('month', CURRENT_DATE)
		)
	`
	var exists bool
	err := r.db.QueryRow(ctx, query, reviewerID, targetID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check existing review: %w", err)
	}
	return exists, nil
}

// GetMonthlyStats aggregates scores for the current month
func (r *ReviewRepository) GetMonthlyStats(ctx context.Context, userID int64) (*domain.UserReviewStats, error) {
	query := `
		SELECT 
			COUNT(*),
			COALESCE(AVG(data_fluency), 0),
			COALESCE(AVG(project_management), 0),
			COALESCE(AVG(digital_proficiency), 0),
			COALESCE(AVG(crisis_communication), 0),
			COALESCE(AVG(service_design_thinking), 0)
		FROM user_reviews
		WHERE target_user_id = $1
		  AND date_trunc('month', created_at) = date_trunc('month', CURRENT_DATE)
	`

	stats := &domain.UserReviewStats{
		TargetUserID: userID,
		Period:       "current_month",
	}

	err := r.db.QueryRow(ctx, query, userID).Scan(
		&stats.ReviewCount,
		&stats.AvgDataFluency,
		&stats.AvgProjectManagement,
		&stats.AvgDigitalProficiency,
		&stats.AvgCrisisCommunication,
		&stats.AvgServiceDesign,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return stats, nil
		}
		return nil, fmt.Errorf("get review stats: %w", err)
	}

	return stats, nil
}

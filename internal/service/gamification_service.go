package service

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type GamificationService struct {
	repo   *repository.GamificationRepository
	logger *zap.Logger
}

func NewGamificationService(repo *repository.GamificationRepository, logger *zap.Logger) *GamificationService {
	return &GamificationService{
		repo:   repo,
		logger: logger.With(zap.String("service", "gamification")),
	}
}

func (s *GamificationService) TrackActivity(ctx context.Context, userID int64, actionType string) error {
	points, exists := domain.GamificationPoints[actionType]
	if !exists {
		s.logger.Warn("Action type not found in gamification points", zap.String("action_type", actionType))
		return nil // Graceful skip
	}

	err := s.repo.AddActivity(ctx, userID, actionType, points)
	if err != nil {
		s.logger.Error("Failed to add gamification activity",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("action_type", actionType),
		)
		return err
	}

	s.logger.Debug("Awarded gamification points",
		zap.Int64("user_id", userID),
		zap.String("action_type", actionType),
		zap.Int("points", points),
	)

	return nil
}

// TrackActivityOncePerDay is like TrackActivity but awards points at most once
// per calendar day for the given actionType.
// Returns nil (no error) when the action has already been tracked today.
func (s *GamificationService) TrackActivityOncePerDay(ctx context.Context, userID int64, actionType string) error {
	already, err := s.repo.HasActivityToday(ctx, userID, actionType)
	if err != nil {
		s.logger.Warn("Failed to check today's activity, skipping gamification",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("action_type", actionType),
		)
		return nil // Graceful: don't block the main flow
	}
	if already {
		s.logger.Debug("Daily gamification already awarded, skipping",
			zap.Int64("user_id", userID),
			zap.String("action_type", actionType),
		)
		return nil
	}
	return s.TrackActivity(ctx, userID, actionType)
}

func (s *GamificationService) GetDashboard(ctx context.Context, limit int) ([]domain.UserGamificationDashboard, error) {
	if limit <= 0 || limit > 200 {
		limit = 50 // Default limit to protect performance
	}
	return s.repo.GetDashboard(ctx, limit)
}

func (s *GamificationService) GetMyStats(ctx context.Context, userID int64, startDate, endDate string) (*domain.UserGamificationDashboard, error) {
	return s.repo.GetMyStats(ctx, userID, startDate, endDate)
}

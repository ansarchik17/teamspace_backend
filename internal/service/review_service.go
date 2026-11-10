package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ReviewRepo interface {
	Create(ctx context.Context, review *domain.UserReview) error
	HasReviewedThisMonth(ctx context.Context, reviewerID, targetID int64) (bool, error)
	GetMonthlyStats(ctx context.Context, userID int64) (*domain.UserReviewStats, error)
}

type TaskRepoForReview interface {
	HasAnyInteractionBetweenUsers(ctx context.Context, user1ID, user2ID int64) (bool, error)
}

type CalendarRepoForReview interface {
	HasAnyInteractionBetweenUsers(ctx context.Context, user1ID, user2ID int64) (bool, error)
}

type ChatRepoForReview interface {
	GetConversationByParticipants(ctx context.Context, user1ID, user2ID int64) (*domain.Conversation, error)
}

type ReviewService struct {
	reviewRepo   ReviewRepo
	chatRepo     ChatRepoForReview
	taskRepo     TaskRepoForReview
	calendarRepo CalendarRepoForReview
	gamification *GamificationService // новое поле
	logger       *zap.Logger
}

func NewReviewService(
	reviewRepo ReviewRepo,
	chatRepo ChatRepoForReview,
	taskRepo TaskRepoForReview,
	calendarRepo CalendarRepoForReview,
	gamification *GamificationService,
	logger *zap.Logger,
) *ReviewService {
	return &ReviewService{
		reviewRepo:   reviewRepo,
		chatRepo:     chatRepo,
		taskRepo:     taskRepo,
		calendarRepo: calendarRepo,
		gamification: gamification,
		logger:       logger.With(zap.String("service", "review")),
	}
}

func (s *ReviewService) CheckReviewStatus(ctx context.Context, reviewerID, targetID int64) (bool, error) {
	return s.reviewRepo.HasReviewedThisMonth(ctx, reviewerID, targetID)
}

func (s *ReviewService) hasAnyInteraction(ctx context.Context, reviewerID, targetID int64) (bool, error) {
	conv, err := s.chatRepo.GetConversationByParticipants(ctx, reviewerID, targetID)
	if err != nil {
		return false, err
	}
	if conv != nil {
		return true, nil
	}

	if s.taskRepo != nil {
		ok, err := s.taskRepo.HasAnyInteractionBetweenUsers(ctx, reviewerID, targetID)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}

	if s.calendarRepo != nil {
		ok, err := s.calendarRepo.HasAnyInteractionBetweenUsers(ctx, reviewerID, targetID)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}

	return false, nil
}

func (s *ReviewService) CreateReview(ctx context.Context, reviewerID int64, req domain.CreateReviewRequest) error {
	if reviewerID == req.TargetUserID {
		return apperrors.BadRequest("cannot review yourself")
	}

	ok, err := s.hasAnyInteraction(ctx, reviewerID, req.TargetUserID)
	if err != nil {
		return apperrors.InternalWithErr("failed to verify interaction", err)
	}
	if !ok {
		return apperrors.Forbidden("Вы можете оставлять отзывы только тем пользователям, с которыми у вас были общие задачи, встречи или чаты")
	}

	exists, err := s.reviewRepo.HasReviewedThisMonth(ctx, reviewerID, req.TargetUserID)
	if err != nil {
		return apperrors.InternalWithErr("failed to check limits", err)
	}
	if exists {
		return apperrors.BadRequest("you have already reviewed this user this month")
	}

	review := &domain.UserReview{
		ID:                  uuid.New(),
		ReviewerID:          reviewerID,
		TargetUserID:        req.TargetUserID,
		DataFluency:         req.DataFluency,
		ProjectManagement:   req.ProjectManagement,
		DigitalProficiency:  req.DigitalProficiency,
		CrisisCommunication: req.CrisisCommunication,
		ServiceDesign:       req.ServiceDesign,
		CreatedAt:           time.Now(),
	}

	if err := s.reviewRepo.Create(ctx, review); err != nil {
		return apperrors.InternalWithErr("failed to save review", err)
	}

	// Гамификация: оставил ревью коллеге
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), reviewerID, domain.ActionReviewGiven) }()
	}

	s.logger.Info("Review submitted", zap.Int64("reviewer", reviewerID), zap.Int64("target", req.TargetUserID))
	return nil
}

func (s *ReviewService) GetMyMonthlyStats(ctx context.Context, userID int64) (*domain.UserReviewStats, error) {
	return s.reviewRepo.GetMonthlyStats(ctx, userID)
}

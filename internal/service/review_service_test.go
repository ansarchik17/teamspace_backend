package service

import (
	"context"
	"testing"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type reviewRepoStub struct{}

func (reviewRepoStub) Create(ctx context.Context, review *domain.UserReview) error { return nil }
func (reviewRepoStub) HasReviewedThisMonth(ctx context.Context, reviewerID, targetID int64) (bool, error) {
	return false, nil
}
func (reviewRepoStub) GetMonthlyStats(ctx context.Context, userID int64) (*domain.UserReviewStats, error) {
	return nil, nil
}

type taskRepoStub struct {
	hasInteraction bool
}

func (s taskRepoStub) HasAnyInteractionBetweenUsers(ctx context.Context, user1ID, user2ID int64) (bool, error) {
	return s.hasInteraction, nil
}

type calendarRepoStub struct {
	hasInteraction bool
}

func (s calendarRepoStub) HasAnyInteractionBetweenUsers(ctx context.Context, user1ID, user2ID int64) (bool, error) {
	return s.hasInteraction, nil
}

type chatRepoStub struct {
	conversation *domain.Conversation
}

func (s chatRepoStub) GetConversationByParticipants(ctx context.Context, user1ID, user2ID int64) (*domain.Conversation, error) {
	return s.conversation, nil
}

func TestCreateReviewReturnsLocalizedForbiddenMessageWhenNoInteraction(t *testing.T) {
	t.Parallel()

	svc := NewReviewService(
		reviewRepoStub{},
		chatRepoStub{},
		taskRepoStub{},
		calendarRepoStub{},
		nil,
		zap.NewNop(),
	)

	err := svc.CreateReview(context.Background(), 142, domain.CreateReviewRequest{
		TargetUserID:        52,
		DataFluency:         5,
		ProjectManagement:   5,
		DigitalProficiency:  5,
		CrisisCommunication: 5,
		ServiceDesign:       5,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	appErr := apperrors.ToAppError(err)
	if appErr.Code != apperrors.ErrCodeForbidden {
		t.Fatalf("expected forbidden error, got %s", appErr.Code)
	}

	want := "Вы можете оставлять отзывы только тем пользователям, с которыми у вас были общие задачи, встречи или чаты"
	if appErr.Message != want {
		t.Fatalf("expected localized message %q, got %q", want, appErr.Message)
	}
}

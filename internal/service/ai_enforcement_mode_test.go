package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
)

func TestAIPaidCall_AccountingDisabled(t *testing.T) {
	org := uuid.New()
	userID := int64(10)

	store := &aiAccountingStub{
		reserveErr: domain.ErrAIAccountingDisabled,
	}
	svc := NewAIPaidCallerService(store)

	caller, err := domain.NewAIPaidUserCaller(org, userID)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	calls := 0
	_, err = svc.ExecutePaidCall(ctx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})

	require.ErrorIs(t, err, domain.ErrAIAccountingDisabled)
	require.Zero(t, calls, "dispatch must not be called when accounting is disabled")
	require.Zero(t, store.markCalls)
}

func TestAIPaidCall_UnpricedModelRejected(t *testing.T) {
	org := uuid.New()
	userID := int64(10)

	store := &aiAccountingStub{
		reserveErr: domain.ErrAICreditUnknownModel,
	}
	svc := NewAIPaidCallerService(store)

	caller, err := domain.NewAIPaidUserCaller(org, userID)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	calls := 0
	_, err = svc.ExecutePaidCall(ctx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})

	require.ErrorIs(t, err, domain.ErrAICreditUnknownModel)
	require.Zero(t, calls, "dispatch must not be called for unpriced model")
	require.Zero(t, store.markCalls)
}

func TestAIPaidCall_ObserveModeAllowsAndSettlesOverBudget(t *testing.T) {
	org := uuid.New()
	userID := int64(42)
	attemptID := uuid.New()
	opID := uuid.New()

	// В observe mode резервация создается успешно несмотря на превышение лимитов
	store := &aiAccountingStub{
		reservation: domain.AIReservation{
			OperationID: opID,
			AttemptID:   attemptID,
			Created:     true,
		},
	}
	svc := NewAIPaidCallerService(store)

	caller, err := domain.NewAIPaidUserCaller(org, userID)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	inTokens, outTokens := int64(500), int64(200)
	evidence := domain.AISettleRequest{
		ActualModel:         "openai/gpt-oss-120b",
		UsageAdapterVersion: "test-v1",
		UsageSource:         "provider_api",
		Usage: domain.AICreditUsage{
			Status:       "verified",
			InputTokens:  &inTokens,
			OutputTokens: &outTokens,
		},
	}

	calls := 0
	result, err := svc.ExecutePaidCall(ctx, validTestParams(), func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		require.Equal(t, attemptID, reservation.AttemptID)
		return "actual provider output in observe mode", evidence, nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls, "dispatch must be called in observe mode")
	require.Equal(t, 1, store.reserveCalls)
	require.Equal(t, 1, store.markCalls)
	require.Len(t, store.completed, 1)
	require.Equal(t, "actual provider output in observe mode", result.Response)
	require.Equal(t, "settled", result.Reservation.AttemptStatus)
	require.NotNil(t, result.Settlement)
	require.True(t, result.Settlement.Applied)
}

func TestAIPaidCall_EnforcedModeRejectsOverBudget(t *testing.T) {
	denialsBefore := metrics.DefaultAIAccounting().CounterValue("quota_denials", "scope", "company")
	org := uuid.New()
	userID := int64(42)

	// В enforced mode хранилище возвращает отказ по лимиту
	deniedErr := &domain.AIReservationDenied{
		Reason: "COMPANY_WEEKLY_EXHAUSTED",
		Scope:  "company",
	}
	store := &aiAccountingStub{
		reserveErr: deniedErr,
	}
	svc := NewAIPaidCallerService(store)

	caller, err := domain.NewAIPaidUserCaller(org, userID)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	calls := 0
	_, err = svc.ExecutePaidCall(ctx, validTestParams(), func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})

	require.ErrorIs(t, err, deniedErr)
	require.Zero(t, calls, "dispatch must never be called when reservation is denied")
	require.Zero(t, store.markCalls)
	require.Empty(t, store.completed)
	require.Equal(t, denialsBefore+1, metrics.DefaultAIAccounting().CounterValue("quota_denials", "scope", "company"))
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIFailureExecutorContexts(t *testing.T) {
	for _, scenario := range []string{"before_dispatch", "client_cancelled", "timeout", "error_with_usage", "usage_after_cancel", "invalid_usage", "already_claimed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			org, attempt := uuid.New(), uuid.New()
			store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: attempt, Created: true}}
			if scenario == "before_dispatch" {
				store.onReserve = cancel
			}
			if scenario == "already_claimed" {
				store.markFalse = true
			}
			if scenario == "invalid_usage" {
				store.settleErr = domain.ErrAICreditUnknownUsage
			}
			i, o := int64(2), int64(1)
			usage := domain.AISettleRequest{ActualModel: "openai/gpt-oss-120b", UsageAdapterVersion: "test-v1", UsageSource: "provider_usage", Usage: domain.AICreditUsage{Status: "verified", InputTokens: &i, OutputTokens: &o}}
			calls := 0
			_, _, err := NewAIAccountingService(store).RunSuccess(ctx, domain.AIReserveRequest{OrganizationID: org}, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
				calls++
				switch scenario {
				case "client_cancelled":
					cancel()
					return domain.AISettleRequest{}, context.Canceled
				case "timeout":
					return domain.AISettleRequest{}, context.DeadlineExceeded
				case "error_with_usage":
					cancel()
					return usage, errors.New("provider error with verified usage")
				case "usage_after_cancel":
					cancel()
					return usage, nil
				default:
					return domain.AISettleRequest{}, nil
				}
			})
			if scenario == "before_dispatch" || scenario == "already_claimed" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			if scenario == "usage_after_cancel" {
				require.NoError(t, err)
				require.Len(t, store.completed, 1)
				require.NoError(t, store.settleContextErrors[0], "accounting must survive client cancellation")
				require.Empty(t, store.failures)
				return
			}
			require.Error(t, err)
			if scenario == "already_claimed" {
				require.ErrorIs(t, err, domain.ErrAIReservationConflict)
				require.Empty(t, store.failures)
				return
			}
			require.Len(t, store.failures, 1)
			failure := store.failures[0]
			require.Equal(t, org, failure.OrganizationID)
			require.Equal(t, attempt, failure.AttemptID)
			require.NoError(t, store.failureContextErrors[0])
			switch scenario {
			case "before_dispatch":
				require.Equal(t, domain.AIFailureNotDispatched, failure.Outcome)
				require.Equal(t, "local_dispatch_guard", failure.EvidenceSource)
				require.Zero(t, store.markCalls)
			case "error_with_usage":
				require.Equal(t, domain.AIFailureCharged, failure.Outcome)
				require.NotNil(t, failure.Usage)
				require.Equal(t, attempt, failure.Usage.AttemptID)
			default:
				require.Equal(t, domain.AIFailureUnknown, failure.Outcome)
				require.Nil(t, failure.Usage)
			}
			if scenario == "timeout" {
				require.Equal(t, "timeout", failure.Reason)
			}
			if scenario == "client_cancelled" {
				require.Equal(t, "client_cancelled", failure.Reason)
			}
		})
	}
}

func TestAIFailureFallbackRetainsUnknown(t *testing.T) {
	for _, outcome := range []domain.AIFailureOutcome{domain.AIFailureCharged, domain.AIFailureNotDispatched} {
		t.Run(string(outcome), func(t *testing.T) {
			store := &aiAccountingStub{failureErr: domain.ErrAISettlementConflict}
			attempt := uuid.New()
			err := recordAIDispatchFailure(context.Background(), store, domain.AIReserveRequest{OrganizationID: uuid.New()}, domain.AIReservation{AttemptID: attempt}, outcome, &domain.AISettleRequest{}, context.Canceled)
			require.ErrorIs(t, err, domain.ErrAISettlementConflict)
			require.Len(t, store.failures, 2)
			require.Equal(t, domain.AIFailureUnknown, store.failures[1].Outcome)
			require.Nil(t, store.failures[1].Usage)
			require.NotEqual(t, store.failures[0].EventID, store.failures[1].EventID)
		})
	}
}

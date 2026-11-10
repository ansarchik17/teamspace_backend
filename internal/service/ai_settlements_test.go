package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
)

type aiAccountingStub struct {
	reservation          domain.AIReservation
	reserveErr           error
	reserveCalls         int
	settleErr            error
	completed            []domain.AISettleRequest
	failures             []domain.AIFailureRequest
	failureContextErrors []error
	settleContextErrors  []error
	markCalls            int
	markFalse            bool
	onReserve            func()
	failureErr           error
}

func (s *aiAccountingStub) Reserve(context.Context, domain.AIReserveRequest) (domain.AIReservation, error) {
	s.reserveCalls++
	if s.onReserve != nil {
		s.onReserve()
	}
	return s.reservation, s.reserveErr
}
func (s *aiAccountingStub) FindOperation(context.Context, domain.AIOperationLookup) (*domain.AIOperationView, error) {
	return nil, nil
}
func (s *aiAccountingStub) MarkSent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	s.markCalls++
	return !s.markFalse, nil
}
func (s *aiAccountingStub) FinishFailure(ctx context.Context, req domain.AIFailureRequest) (domain.AIFailureResult, error) {
	s.failures = append(s.failures, req)
	s.failureContextErrors = append(s.failureContextErrors, ctx.Err())
	if s.failureErr != nil && req.Outcome != domain.AIFailureUnknown {
		return domain.AIFailureResult{}, s.failureErr
	}
	return domain.AIFailureResult{Applied: true}, nil
}
func (s *aiAccountingStub) SettleSuccess(ctx context.Context, req domain.AISettleRequest) (domain.AISettlement, error) {
	s.completed = append(s.completed, req)
	s.settleContextErrors = append(s.settleContextErrors, ctx.Err())
	return domain.AISettlement{AttemptID: req.AttemptID, Applied: true}, s.settleErr
}

func TestAIAccountingSuccessfulDispatch(t *testing.T) {
	providerErr := errors.New("unknown provider outcome")
	settleErr := errors.New("commit unavailable")
	for _, tc := range []struct {
		name                               string
		created                            bool
		reserveErr, dispatchErr, settleErr error
		calls, completions                 int
	}{
		{"denied", false, domain.ErrAIReservationInvalid, nil, nil, 0, 0},
		{"replay", false, nil, nil, nil, 0, 0},
		{"provider failure", true, nil, providerErr, nil, 1, 0},
		{"unknown usage", true, nil, nil, domain.ErrAICreditUnknownUsage, 1, 1},
		{"settlement failure", true, nil, nil, settleErr, 1, 1},
		{"success", true, nil, nil, nil, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeSuccess := metrics.DefaultAIAccounting().CounterValue("operations", "outcome", "success")
			beforeUnknown := metrics.DefaultAIAccounting().CounterValue("operations", "outcome", "unknown")
			attempt := uuid.New()
			org := uuid.New()
			store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: attempt, Created: tc.created}, reserveErr: tc.reserveErr, settleErr: tc.settleErr}
			s := NewAIAccountingService(store)
			calls := 0
			reservation, settlement, err := s.RunSuccess(context.Background(), domain.AIReserveRequest{OrganizationID: org}, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
				calls++
				return domain.AISettleRequest{OrganizationID: uuid.New(), AttemptID: uuid.New(), UsageSource: "provider"}, tc.dispatchErr
			})
			require.Equal(t, tc.calls, calls)
			require.Len(t, store.completed, tc.completions)
			for _, req := range store.completed {
				require.Equal(t, attempt, req.AttemptID)
				require.Equal(t, org, req.OrganizationID)
			}
			expected := tc.reserveErr
			if tc.dispatchErr != nil {
				expected = tc.dispatchErr
			}
			if tc.settleErr != nil {
				expected = tc.settleErr
			}
			require.ErrorIs(t, err, expected)
			if tc.created && tc.reserveErr == nil {
				require.True(t, reservation.Created)
			}
			if expected == nil && tc.created {
				require.NotNil(t, settlement)
			} else {
				require.Nil(t, settlement)
			}
			if tc.name == "success" {
				require.Equal(t, beforeSuccess+1, metrics.DefaultAIAccounting().CounterValue("operations", "outcome", "success"))
			}
			if tc.name == "provider failure" || tc.name == "unknown usage" || tc.name == "settlement failure" {
				require.Equal(t, beforeUnknown+1, metrics.DefaultAIAccounting().CounterValue("operations", "outcome", "unknown"))
			}
		})
	}
}

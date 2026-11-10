package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type aiReservationStub struct {
	result domain.AIReservation
	err    error
}

func (s aiReservationStub) Reserve(context.Context, domain.AIReserveRequest) (domain.AIReservation, error) {
	return s.result, s.err
}
func (s aiReservationStub) FindOperation(context.Context, domain.AIOperationLookup) (*domain.AIOperationView, error) {
	return nil, nil
}
func (s aiReservationStub) MarkSent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}
func (s aiReservationStub) FinishFailure(context.Context, domain.AIFailureRequest) (domain.AIFailureResult, error) {
	return domain.AIFailureResult{}, nil
}

func TestAIReservedDispatch(t *testing.T) {
	denied := &domain.AIReservationDenied{Reason: "FIVE_HOUR_EXHAUSTED"}
	for _, tc := range []struct {
		name       string
		created    bool
		reserveErr error
		calls      int
	}{
		{"denied", false, denied, 0}, {"replay", false, nil, 0}, {"accepted", true, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			providerErr := errors.New("unknown provider outcome")
			s := NewAIReservationService(aiReservationStub{domain.AIReservation{Created: tc.created}, tc.reserveErr})
			out, err := s.Run(context.Background(), domain.AIReserveRequest{}, func(context.Context, domain.AIReservation) error { calls++; return providerErr })
			require.Equal(t, tc.calls, calls)
			if tc.created {
				require.True(t, out.Created)
				require.ErrorIs(t, err, providerErr)
			} else {
				require.ErrorIs(t, err, tc.reserveErr)
			}
		})
	}
}

package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type aiOperationLookupStub struct {
	aiAccountingStub
	lookups        int
	findErr        error
	view           *domain.AIOperationView
	initialMissing bool
}

func (s *aiOperationLookupStub) FindOperation(context.Context, domain.AIOperationLookup) (*domain.AIOperationView, error) {
	s.lookups++
	if s.initialMissing && s.lookups == 1 {
		return nil, nil
	}
	return s.view, s.findErr
}

func TestAIOperationLookupBeforeGeneration(t *testing.T) {
	for _, scenario := range []string{"existing", "racing_creation", "lookup_denied"} {
		t.Run(scenario, func(t *testing.T) {
			id := uuid.New()
			stored := &domain.AISettlement{ChargedMicrocredits: 5000}
			store := &aiOperationLookupStub{view: &domain.AIOperationView{LatestAttempt: domain.AIReservation{OperationID: id, Settlement: stored}}}
			if scenario == "racing_creation" {
				store.initialMissing = true
				store.reserveErr = domain.ErrAIReservationConflict
			}
			if scenario == "lookup_denied" {
				store.findErr = &domain.AIReservationDenied{Reason: "AI_ACCESS_DENIED"}
			}
			calls := 0
			out, result, err := NewAIAccountingService(store).RunSuccess(context.Background(), domain.AIReserveRequest{AttemptNumber: 1}, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
				calls++
				return domain.AISettleRequest{}, nil
			})
			if scenario == "lookup_denied" {
				require.ErrorIs(t, err, store.findErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, id, out.OperationID)
				require.Equal(t, stored, result)
			}
			require.Zero(t, calls)
			require.Zero(t, store.markCalls)
			if scenario == "racing_creation" {
				require.Equal(t, 1, store.reserveCalls)
				require.Equal(t, 2, store.lookups)
			} else {
				require.Zero(t, store.reserveCalls)
			}
		})
	}
}

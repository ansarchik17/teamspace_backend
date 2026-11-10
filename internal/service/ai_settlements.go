package service

import (
	"context"
	"errors"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
)

type AIAccountingStore interface {
	AIReservationStore
	SettleSuccess(context.Context, domain.AISettleRequest) (domain.AISettlement, error)
}

type AIAccountingService struct{ store AIAccountingStore }

func NewAIAccountingService(store AIAccountingStore) *AIAccountingService {
	return &AIAccountingService{store: store}
}

// Complete can be retried safely when the commit result was lost. A caller must
// retain the original normalized evidence; changed evidence is a conflict.
func (s *AIAccountingService) Complete(ctx context.Context, req domain.AISettleRequest) (domain.AISettlement, error) {
	return completeAISettlement(ctx, s.store, req)
}

func completeAISettlement(ctx context.Context, store AIAccountingStore, req domain.AISettleRequest) (domain.AISettlement, error) {
	settlement, err := store.SettleSuccess(ctx, req)
	if err != nil {
		metrics.DefaultAIAccounting().Inc("settlement_errors", "kind", aiMetricErrorKind(err))
		return settlement, err
	}
	if settlement.Applied {
		metrics.DefaultAIAccounting().Inc("operations", "outcome", "success")
	}
	return settlement, nil
}

// RunSuccess connects one bounded provider attempt to reservation and successful
// settlement. No network work occurs inside a DB transaction. Dispatch/usage
// errors retain the reservation for reconciliation, never assume zero usage.
// Evidence identity is bound to this reservation, not taken from the callback.
func (s *AIAccountingService) RunSuccess(ctx context.Context, req domain.AIReserveRequest, dispatch func(context.Context, domain.AIReservation) (domain.AISettleRequest, error)) (domain.AIReservation, *domain.AISettlement, error) {
	if dispatch == nil {
		return domain.AIReservation{}, nil, domain.ErrAIReservationInvalid
	}
	reservation, err := reserveAIOperation(ctx, s.store, req)
	if err != nil || !reservation.Created {
		return reservation, reservation.Settlement, err
	}
	if err = ctx.Err(); err != nil {
		return reservation, nil, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, domain.AIFailureNotDispatched, nil, err))
	}
	claimed, err := s.store.MarkSent(ctx, req.OrganizationID, reservation.AttemptID)
	if err != nil {
		return reservation, nil, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, domain.AIFailureNotDispatched, nil, err))
	}
	if !claimed {
		return reservation, nil, domain.ErrAIReservationConflict
	}
	reservation.AttemptStatus = "sent"
	reservation.OperationStatus = "running"
	reservation.Disposition = domain.AIOperationInProgress
	evidence, err := dispatch(ctx, reservation)
	evidence.OrganizationID = req.OrganizationID
	evidence.AttemptID = reservation.AttemptID
	if err != nil {
		outcome := domain.AIFailureUnknown
		var usage *domain.AISettleRequest
		if evidence.Usage.HasKnownCounters() {
			outcome, usage = domain.AIFailureCharged, &evidence
		}
		return reservation, nil, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, outcome, usage, err))
	}
	// Usage that arrived despite client cancellation must still be accounted.
	accountingCtx, cancel := aiFailureAccountingContext(ctx)
	defer cancel()
	settlement, err := s.Complete(accountingCtx, evidence)
	if err != nil {
		return reservation, nil, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, domain.AIFailureUnknown, nil, err))
	}
	reservation.AttemptStatus = "settled"
	reservation.ReservationStatus = "settled"
	reservation.OperationStatus = settlement.OperationStatus
	reservation.Disposition = domain.AIReplayDisposition(settlement.OperationStatus, "settled")
	reservation.Settlement = &settlement
	return reservation, &settlement, nil
}

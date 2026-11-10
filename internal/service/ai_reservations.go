package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type AIReservationStore interface {
	FindOperation(context.Context, domain.AIOperationLookup) (*domain.AIOperationView, error)
	Reserve(context.Context, domain.AIReserveRequest) (domain.AIReservation, error)
	MarkSent(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	FinishFailure(context.Context, domain.AIFailureRequest) (domain.AIFailureResult, error)
}

type AIReservationService struct{ store AIReservationStore }

func NewAIReservationService(store AIReservationStore) *AIReservationService {
	return &AIReservationService{store: store}
}

// Run reserves and commits before dispatching a single provider attempt. The
// adapter must bound the request to req.Microcredits and perform no hidden retry.
// Replays never dispatch. Dispatch errors/cancellation retain the hold: they do
// not prove the provider did not execute. Settlement is a separate operation.
func (s *AIReservationService) Run(ctx context.Context, req domain.AIReserveRequest, dispatch func(context.Context, domain.AIReservation) error) (domain.AIReservation, error) {
	if dispatch == nil {
		return domain.AIReservation{}, domain.ErrAIReservationInvalid
	}
	reservation, err := reserveAIOperation(ctx, s.store, req)
	if err != nil || !reservation.Created {
		return reservation, err
	}
	if err = ctx.Err(); err != nil {
		return reservation, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, domain.AIFailureNotDispatched, nil, err))
	}
	claimed, err := s.store.MarkSent(ctx, req.OrganizationID, reservation.AttemptID)
	if err != nil {
		return reservation, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, domain.AIFailureNotDispatched, nil, err))
	}
	if !claimed {
		return reservation, domain.ErrAIReservationConflict
	}
	reservation.AttemptStatus = "sent"
	reservation.OperationStatus = "running"
	reservation.Disposition = domain.AIOperationInProgress
	err = dispatch(ctx, reservation)
	if err != nil {
		return reservation, errors.Join(err, recordAIDispatchFailure(ctx, s.store, req, reservation, domain.AIFailureUnknown, nil, err))
	}
	return reservation, nil
}

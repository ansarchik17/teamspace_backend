package service

import (
	"context"
	"errors"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// AIPaidDispatchFunc is the provider invocation callback. It receives the active,
// reserved attempt and must return the provider response alongside normalized settlement evidence.
type AIPaidDispatchFunc func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error)

// AIPaidExecutionResult contains the provider response and settled/replay accounting metadata.
// A non-nil result may accompany an error. Reservation is the last known snapshot,
// not confirmation that failure accounting or settlement has committed.
type AIPaidExecutionResult struct {
	Response    any
	Reservation domain.AIReservation
	Settlement  *domain.AISettlement
}

// AIPaidCallerService is the reusable interface unifying reservation, provider invocation,
// expense settlement, and error/cancellation accounting across all AI agents and tools.
// Calls without verified server context are strictly blocked and cannot bypass accounting.
type AIPaidCallerService interface {
	ExecutePaidCall(ctx context.Context, params domain.AIPaidCallParams, dispatch AIPaidDispatchFunc) (*AIPaidExecutionResult, error)
}

type aiPaidCallerServiceImpl struct {
	store   AIAccountingStore
	limiter AIGenerationLimiter
}

// AIPaidCallerOption allows configuring optional extensions on AIPaidCallerService.
type AIPaidCallerOption func(*aiPaidCallerServiceImpl)

// WithAILimiter injects an AIGenerationLimiter to enforce concurrency and frequency limits before reservation.
func WithAILimiter(limiter AIGenerationLimiter) AIPaidCallerOption {
	return func(s *aiPaidCallerServiceImpl) {
		s.limiter = limiter
	}
}

// NewAIPaidCallerService constructs a reusable AIPaidCallerService backed by the accounting store.
func NewAIPaidCallerService(store AIAccountingStore, opts ...AIPaidCallerOption) AIPaidCallerService {
	svc := &aiPaidCallerServiceImpl{store: store}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

// ExecutePaidCall executes a paid AI call through the unified lifecycle:
// 1. Context validation (strictly verifies caller identity & company; unauthenticated calls are rejected immediately).
// 2. Pre-reservation limit enforcement (checks rate limit and acquires concurrency lease before reservation).
// 3. Reservation & idempotency lookup (replay returns recorded outcome without invoking dispatch).
// 4. Dispatch guard & MarkSent (atomic claim before provider communication).
// 5. Provider invocation with original context.
// 6. Outcome settlement or failure recording in an isolated DB accounting context.
func (s *aiPaidCallerServiceImpl) ExecutePaidCall(ctx context.Context, params domain.AIPaidCallParams, dispatch AIPaidDispatchFunc) (*AIPaidExecutionResult, error) {
	if dispatch == nil {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}

	// 1. Verify context & caller identity
	caller, err := params.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	leaseErrors := make(chan error, 1)

	// 2. Pre-reservation limit enforcement (Rate limit & Concurrency slots per authenticated user)
	// Must check limits BEFORE creating any reservation in the DB or contacting the provider!
	if s.limiter != nil && caller.UserID != nil {
		lease, limitErr := s.limiter.Acquire(ctx, *caller.UserID)
		if limitErr != nil {
			// Limit rejection fails immediately without writing any reservations to the DB
			return nil, limitErr
		}

		stopHeartbeat := make(chan struct{})
		heartbeatDone := make(chan struct{})
		go runLeaseHeartbeat(lease, s.limiter.HeartbeatInterval(), stopHeartbeat, heartbeatDone, leaseErrors)

		// The concurrency slot is held until actual completion of work and necessary accounting actions.
		// Client HTTP disconnection does NOT release the slot prematurely while work/settlement continues.
		defer func() {
			close(stopHeartbeat)
			<-heartbeatDone
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = lease.Release(releaseCtx)
		}()
	}

	reserveReq, err := params.ToReserveRequest(caller)
	if err != nil {
		return nil, err
	}

	// 2. Reservation with logical replay checking
	reservation, err := reserveAIOperation(ctx, s.store, reserveReq)
	if err != nil || !reservation.Created {
		if err != nil {
			return nil, err
		}
		// Idempotent replay: dispatch is never called. Return stored result if available.
		return &AIPaidExecutionResult{
			Reservation: reservation,
			Settlement:  reservation.Settlement,
		}, nil
	}

	// 3. Dispatch guard check prior to marking sent
	if err = ctx.Err(); err != nil {
		recordErr := recordAIDispatchFailure(ctx, s.store, reserveReq, reservation, domain.AIFailureNotDispatched, nil, err)
		return nil, errors.Join(err, recordErr)
	}
	select {
	case leaseErr := <-leaseErrors:
		err = errors.Join(domain.ErrAILimiterUnavailable, leaseErr)
		recordErr := recordAIDispatchFailure(ctx, s.store, reserveReq, reservation, domain.AIFailureNotDispatched, nil, err)
		return &AIPaidExecutionResult{Reservation: reservation}, errors.Join(err, recordErr)
	default:
	}

	// Atomic mark sent
	claimed, err := s.store.MarkSent(ctx, reserveReq.OrganizationID, reservation.AttemptID)
	if err != nil {
		recordErr := recordAIDispatchFailure(ctx, s.store, reserveReq, reservation, domain.AIFailureNotDispatched, nil, err)
		return nil, errors.Join(err, recordErr)
	}
	if !claimed {
		return nil, domain.ErrAIReservationConflict
	}

	reservation.AttemptStatus = "sent"
	reservation.OperationStatus = "running"
	reservation.Disposition = domain.AIOperationInProgress

	// 4. Invoke provider dispatch
	response, evidence, dispatchErr := dispatch(ctx, reservation)
	evidence.OrganizationID = reserveReq.OrganizationID
	evidence.AttemptID = reservation.AttemptID

	// 5. Error handling
	if dispatchErr != nil {
		outcome := domain.AIFailureUnknown
		var usage *domain.AISettleRequest
		if evidence.Usage.HasKnownCounters() {
			outcome, usage = domain.AIFailureCharged, &evidence
		}
		recordErr := recordAIDispatchFailure(ctx, s.store, reserveReq, reservation, outcome, usage, dispatchErr)
		return &AIPaidExecutionResult{Response: response, Reservation: reservation}, errors.Join(dispatchErr, recordErr)
	}

	// 6. Settlement of verified usage in detached context
	accountingCtx, cancel := aiFailureAccountingContext(ctx)
	defer cancel()
	settlement, err := completeAISettlement(accountingCtx, s.store, evidence)
	if err != nil {
		recordErr := recordAIDispatchFailure(ctx, s.store, reserveReq, reservation, domain.AIFailureUnknown, nil, err)
		return &AIPaidExecutionResult{Response: response, Reservation: reservation}, errors.Join(err, recordErr)
	}

	reservation.AttemptStatus = "settled"
	reservation.ReservationStatus = "settled"
	reservation.OperationStatus = settlement.OperationStatus
	reservation.Disposition = domain.AIReplayDisposition(settlement.OperationStatus, "settled")
	reservation.Settlement = &settlement

	return &AIPaidExecutionResult{
		Response:    response,
		Reservation: reservation,
		Settlement:  &settlement,
	}, nil
}

// ExecutePaidCall attaches the unified paid execution interface to AIAccountingService as well.
func (s *AIAccountingService) ExecutePaidCall(ctx context.Context, params domain.AIPaidCallParams, dispatch AIPaidDispatchFunc) (*AIPaidExecutionResult, error) {
	executor := NewAIPaidCallerService(s.store)
	return executor.ExecutePaidCall(ctx, params, dispatch)
}

// ExecutePaidCallTyped is a generic convenience wrapper allowing callers to receive a strongly typed response.
func ExecutePaidCallTyped[T any](
	ctx context.Context,
	service AIPaidCallerService,
	params domain.AIPaidCallParams,
	dispatch func(ctx context.Context, reservation domain.AIReservation) (T, domain.AISettleRequest, error),
) (T, domain.AIReservation, *domain.AISettlement, error) {
	var zero T
	if dispatch == nil {
		return zero, domain.AIReservation{}, nil, domain.ErrAIPaidCallInvalidRequest
	}

	res, err := service.ExecutePaidCall(ctx, params, func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
		return dispatch(ctx, reservation)
	})
	if res == nil {
		return zero, domain.AIReservation{}, nil, err
	}

	if res.Response != nil {
		if typed, ok := res.Response.(T); ok {
			return typed, res.Reservation, res.Settlement, err
		}
	}

	return zero, res.Reservation, res.Settlement, err
}

func runLeaseHeartbeat(lease AILease, interval time.Duration, stop <-chan struct{}, done chan<- struct{}, failures chan<- error) {
	defer close(done)
	if lease == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := lease.Renew(renewCtx)
			cancel()
			if err != nil {
				select {
				case failures <- err:
				default:
				}
				// Dispatched work remains protected by the PostgreSQL active hold.
				// Never cancel paid work or release that hold merely on lease loss.
				return
			}
		}
	}
}

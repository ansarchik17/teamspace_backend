package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
)

func (s *AIAccountingService) Fail(ctx context.Context, req domain.AIFailureRequest) (domain.AIFailureResult, error) {
	result, err := s.store.FinishFailure(ctx, req)
	if err != nil {
		metrics.DefaultAIAccounting().Inc("settlement_errors", "kind", aiMetricErrorKind(err))
		return result, err
	}
	if result.Applied {
		switch req.Outcome {
		case domain.AIFailureUnknown:
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "unknown")
		case domain.AIFailureNotDispatched, domain.AIFailureNotExecuted:
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "confirmed_not_executed")
		case domain.AIFailureCharged:
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "success")
		}
	}
	return result, nil
}

func aiFailureAccountingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	// Finite DB cleanup survives HTTP disconnect; provider work keeps the
	// original context and never runs in this detached context.
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func recordAIDispatchFailure(ctx context.Context, store AIReservationStore, req domain.AIReserveRequest, reservation domain.AIReservation, outcome domain.AIFailureOutcome, usage *domain.AISettleRequest, cause error) error {
	reason := "provider_error"
	if errors.Is(cause, context.Canceled) {
		reason = "client_cancelled"
	} else if errors.Is(cause, context.DeadlineExceeded) {
		reason = "timeout"
	} else if errors.Is(cause, domain.ErrAICreditUnknownUsage) || errors.Is(cause, domain.ErrAICreditInvalidUsage) || errors.Is(cause, domain.ErrAICreditUnknownModel) || errors.Is(cause, domain.ErrAISettlementInvalid) {
		reason = "invalid_usage"
	}
	source := "executor"
	if outcome == domain.AIFailureNotDispatched {
		source = "local_dispatch_guard"
		if reason == "provider_error" {
			reason = "local_error"
		}
	}
	failure := domain.AIFailureRequest{OrganizationID: req.OrganizationID, AttemptID: reservation.AttemptID, EventID: uuid.NewSHA1(reservation.AttemptID, []byte("executor-failure")), Outcome: outcome, Reason: reason, EvidenceSource: source, Usage: usage}
	accountingCtx, cancel := aiFailureAccountingContext(ctx)
	defer cancel()
	result, err := store.FinishFailure(accountingCtx, failure)
	if err != nil {
		metrics.DefaultAIAccounting().Inc("settlement_errors", "kind", aiMetricErrorKind(err))
	} else if result.Applied {
		switch outcome {
		case domain.AIFailureUnknown:
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "unknown")
		case domain.AIFailureNotDispatched, domain.AIFailureNotExecuted:
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "confirmed_not_executed")
		case domain.AIFailureCharged:
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "success")
		}
	}
	if err != nil && (outcome == domain.AIFailureCharged || outcome == domain.AIFailureNotDispatched) {
		// Invalid usage, an ambiguous dispatch claim or failed accounting commit
		// never makes a call free. Preserve an unknown result when proof fails.
		failure.EventID = uuid.NewSHA1(reservation.AttemptID, []byte("executor-unknown"))
		failure.Outcome = domain.AIFailureUnknown
		failure.Usage = nil
		failure.EvidenceSource = "executor"
		fallback, fallbackErr := store.FinishFailure(accountingCtx, failure)
		if fallbackErr != nil {
			metrics.DefaultAIAccounting().Inc("settlement_errors", "kind", aiMetricErrorKind(fallbackErr))
		} else if fallback.Applied {
			metrics.DefaultAIAccounting().Inc("operations", "outcome", "unknown")
		}
		return errors.Join(err, fallbackErr)
	}
	return err
}

func aiMetricErrorKind(err error) string {
	switch {
	case errors.Is(err, domain.ErrAIReservationInvalid), errors.Is(err, domain.ErrAISettlementInvalid), errors.Is(err, domain.ErrAICreditInvalidUsage):
		return "invalid"
	case errors.Is(err, domain.ErrAIReservationConflict), errors.Is(err, domain.ErrAISettlementConflict):
		return "conflict"
	case errors.Is(err, domain.ErrAICreditUnknownModel), errors.Is(err, domain.ErrAICreditUnknownPolicy), errors.Is(err, domain.ErrAICreditUnknownUsage), errors.Is(err, domain.ErrAICreditOverflow):
		return "calculation"
	default:
		return "database"
	}
}

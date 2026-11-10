package service

import (
	"context"
	"errors"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
)

func (s *AIAccountingService) FindOperation(ctx context.Context, req domain.AIOperationLookup) (*domain.AIOperationView, error) {
	return s.store.FindOperation(ctx, req)
}

// Transport retries always enter with AttemptNumber=1. Only trusted server
// orchestration may request subsequent paid attempts (retry/fallback/tool step).
// Logical replay is checked before new estimates/model routing can change it.
func reserveAIOperation(ctx context.Context, store AIReservationStore, req domain.AIReserveRequest) (domain.AIReservation, error) {
	lookup := domain.AIOperationLookup{OrganizationID: req.OrganizationID, UserID: req.UserID, IdempotencyKey: req.IdempotencyKey, Fingerprint: req.Fingerprint, OperationKind: req.OperationKind, SourceKind: req.SourceKind, SourceID: req.SourceID}
	if req.AttemptNumber == 1 {
		view, err := store.FindOperation(ctx, lookup)
		if err != nil {
			metrics.DefaultAIAccounting().Inc("reservation_errors", "kind", aiMetricErrorKind(err))
			return domain.AIReservation{}, err
		}
		if view != nil {
			return view.LatestAttempt, nil
		}
	}
	out, err := store.Reserve(ctx, req)
	if err != nil {
		var denied *domain.AIReservationDenied
		if errors.As(err, &denied) && isAIFinancialQuotaDenial(denied.Reason) {
			scope := denied.Scope
			switch scope {
			case "company", "weekly", "five_hour", "service", "account":
			default:
				scope = "account"
			}
			metrics.DefaultAIAccounting().Inc("quota_denials", "scope", scope)
		} else if !errors.As(err, &denied) {
			metrics.DefaultAIAccounting().Inc("reservation_errors", "kind", aiMetricErrorKind(err))
		}
	}
	// Two initial lookups may both see no row. Reserve serializes creation; if
	// execution parameters differ, return the now-existing logical operation,
	// never create a new attempt to handle a transport retry.
	if req.AttemptNumber == 1 && errors.Is(err, domain.ErrAIReservationConflict) {
		view, lookupErr := store.FindOperation(ctx, lookup)
		if lookupErr != nil {
			return domain.AIReservation{}, lookupErr
		}
		if view != nil {
			return view.LatestAttempt, nil
		}
	}
	return out, err
}

func isAIFinancialQuotaDenial(reason string) bool {
	switch reason {
	case "COMPANY_BUDGET_UNCONFIGURED", "COMPANY_BUDGET_DISABLED", "COMPANY_WEEKLY_EXHAUSTED",
		"USER_QUOTA_UNCONFIGURED", "USER_QUOTA_DISABLED", "USER_WEEKLY_EXHAUSTED",
		"FIVE_HOUR_DISABLED", "FIVE_HOUR_EXHAUSTED", "SERVICE_QUOTA_DISABLED", "SERVICE_WEEKLY_EXHAUSTED":
		return true
	default:
		return false
	}
}

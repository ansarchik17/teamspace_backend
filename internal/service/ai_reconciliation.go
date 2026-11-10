package service

import (
	"context"
	"errors"
	"time"

	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
	"go.uber.org/zap"
)

type AIReconciliationStore interface {
	ScheduleReconciliation(context.Context, time.Duration, int) (int64, error)
	ClaimReconciliation(context.Context, time.Duration) (*domain.AIReconciliationJob, error)
	ApplyReconciliation(context.Context, domain.AIReconciliationJob, domain.AIReconciliationEvidence, time.Duration, int) (domain.AIReconciliationResult, error)
}

// A usage adapter must implement a confirmed READ-ONLY lookup contract, never
// repeat generation. No default URL/endpoint or guessed tokenizer is permitted.
type AIUsageLookup interface {
	LookupUsage(context.Context, domain.AIReconciliationJob) (domain.AIReconciliationEvidence, error)
}

type AIUsageLookupKey struct{ ProviderID, EndpointKind, UsageAdapterVersion string }

type AIReconciliationWorker struct {
	store   AIReconciliationStore
	cfg     config.AIReconciliationConfig
	lookups map[AIUsageLookupKey]AIUsageLookup
	logger  *zap.Logger
}

type AIReconciliationStats struct {
	Discovered                             int64
	Processed, Completed, Deferred, Manual int
}

func NewAIReconciliationWorker(store AIReconciliationStore, cfg config.AIReconciliationConfig, lookups map[AIUsageLookupKey]AIUsageLookup, logger *zap.Logger) (*AIReconciliationWorker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, domain.ErrAISettlementInvalid
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	registry := make(map[AIUsageLookupKey]AIUsageLookup, len(lookups))
	for key, lookup := range lookups {
		registry[key] = lookup
	}
	return &AIReconciliationWorker{store: store, cfg: cfg, lookups: registry, logger: logger}, nil
}

func (w *AIReconciliationWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		stats, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.Warn("AI reconciliation pass failed; holds retained", zap.String("code", "AI_RECONCILIATION_FAILED"))
		}
		if stats.Processed > 0 {
			w.logger.Info("AI reservation reconciliation pass", zap.Int("completed", stats.Completed), zap.Int("deferred", stats.Deferred), zap.Int("manual", stats.Manual))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *AIReconciliationWorker) RunOnce(ctx context.Context) (AIReconciliationStats, error) {
	var stats AIReconciliationStats
	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	count, err := w.store.ScheduleReconciliation(dbCtx, w.cfg.StaleAfter, w.cfg.BatchSize)
	cancel()
	if err != nil {
		metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciliation_error")
		return stats, err
	}
	stats.Discovered = count
	for i := 0; i < w.cfg.BatchSize; i++ {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		dbCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		job, err := w.store.ClaimReconciliation(dbCtx, w.cfg.LeaseDuration)
		cancel()
		if err != nil {
			metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciliation_error")
			return stats, err
		}
		if job == nil {
			break
		}
		evidence := w.lookup(ctx, *job)
		dbCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		result, err := w.store.ApplyReconciliation(dbCtx, *job, evidence, w.cfg.RetryDelay, w.cfg.MaxClaims)
		cancel()
		if errors.Is(err, domain.ErrAIReconciliationLeaseLost) {
			continue
		}
		if err != nil {
			metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciliation_error")
			return stats, err
		}
		if result.Applied && result.Reason != "already_final" {
			switch {
			case result.JobStatus == "completed" && result.Reason == string(domain.AIReconcileUsage):
				metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciled_usage")
				metrics.DefaultAIAccounting().Inc("operations", "outcome", "success")
			case result.JobStatus == "completed" && (result.Reason == string(domain.AIReconcileNotExecuted) || result.Reason == string(domain.AIReconcileNotDispatched)):
				metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciled_not_executed")
				metrics.DefaultAIAccounting().Inc("operations", "outcome", "confirmed_not_executed")
			case result.JobStatus == "manual":
				metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciliation_manual")
			default:
				metrics.DefaultAIAccounting().Inc("reconciliation", "outcome", "reconciliation_deferred")
			}
		}
		stats.Processed++
		switch result.JobStatus {
		case "completed":
			stats.Completed++
		case "manual":
			stats.Manual++
		default:
			stats.Deferred++
		}
	}
	return stats, nil
}

func (w *AIReconciliationWorker) lookup(ctx context.Context, job domain.AIReconciliationJob) domain.AIReconciliationEvidence {
	if job.ReservationStatus != "active" {
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcilePending, Reason: "state_changed"}
	}
	if job.AttemptStatus == "reserved" && job.PayloadIdentityKnown {
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileNotDispatched}
	}
	if job.ClaimNumber > int64(w.cfg.MaxClaims) {
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "attempts_exhausted"}
	}
	lookup := w.lookups[AIUsageLookupKey{job.ProviderID, job.EndpointKind, job.UsageAdapterVersion}]
	if lookup == nil {
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "lookup_unavailable"}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, w.cfg.LookupTimeout)
	defer cancel()
	evidence, err := lookup.LookupUsage(lookupCtx, job)
	if err != nil {
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcilePending, Reason: "lookup_failed"}
	}
	// A provider adapter cannot claim local non-dispatch, only the DB guard can.
	if evidence.Outcome == domain.AIReconcileNotDispatched {
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "invalid_evidence"}
	}
	switch evidence.Outcome {
	case domain.AIReconcileUsage, domain.AIReconcileNotExecuted, domain.AIReconcilePending, domain.AIReconcileManual:
	default:
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "invalid_evidence"}
	}
	if evidence.Usage != nil {
		usage := *evidence.Usage
		usage.OrganizationID, usage.AttemptID = job.OrganizationID, job.AttemptID
		evidence.Usage = &usage
	}
	return evidence
}

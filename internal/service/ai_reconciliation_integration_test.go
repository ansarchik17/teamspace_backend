package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/metrics"
	"github.com/west6ide/workspace-backend/internal/repository"
)

type aiUsageLookupFunc func(context.Context, domain.AIReconciliationJob) (domain.AIReconciliationEvidence, error)

func (f aiUsageLookupFunc) LookupUsage(ctx context.Context, job domain.AIReconciliationJob) (domain.AIReconciliationEvidence, error) {
	return f(ctx, job)
}

func aiReconciliationTestConfig() config.AIReconciliationConfig {
	return config.AIReconciliationConfig{Enabled: true, Interval: time.Second, StaleAfter: time.Second, LeaseDuration: time.Minute, LookupTimeout: time.Second, RetryDelay: time.Minute, BatchSize: 10, MaxClaims: 3}
}

func TestAIReconciliationWorkers(t *testing.T) {
	for _, scenario := range []string{"usage", "not_executed", "unavailable", "lookup_error", "never_sent"} {
		t.Run(scenario, func(t *testing.T) {
			var outcomeLabel string
			switch scenario {
			case "usage":
				outcomeLabel = "reconciled_usage"
			case "not_executed", "never_sent":
				outcomeLabel = "reconciled_not_executed"
			case "unavailable":
				outcomeLabel = "reconciliation_manual"
			case "lookup_error":
				outcomeLabel = "reconciliation_deferred"
			}
			counterBefore := metrics.DefaultAIAccounting().CounterValue("reconciliation", "outcome", outcomeLabel)
			ctx, db, accounting, org := newAIOperationServiceDB(t)
			data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
			require.NoError(t, err)
			_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
			require.NoError(t, err)
			repo := accounting.store.(*repository.AIReservationRepository)
			first, err := repo.Reserve(ctx, serviceAIOperationRequest(org, "worker-recovery"))
			require.NoError(t, err)
			if scenario != "never_sent" {
				_, err = repo.MarkSent(ctx, org, first.AttemptID)
				require.NoError(t, err)
			}
			_, err = db.Exec(ctx, `UPDATE ai_usage_reservations SET reconcile_after=created_at WHERE attempt_id=$1`, first.AttemptID)
			require.NoError(t, err)
			var lookups atomic.Int32
			registry := map[AIUsageLookupKey]AIUsageLookup{}
			if scenario != "unavailable" {
				registry[AIUsageLookupKey{"nitec", "chat", "test-v1"}] = aiUsageLookupFunc(func(ctx context.Context, job domain.AIReconciliationJob) (domain.AIReconciliationEvidence, error) {
					lookups.Add(1)
					if scenario == "lookup_error" {
						return domain.AIReconciliationEvidence{}, errors.New("provider lookup failed")
					}
					if scenario == "not_executed" {
						proof := "provider-receipt"
						return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileNotExecuted, ProviderConfirmation: &proof}, nil
					}
					usage := serviceAIUsage(job.RequestedModel)
					return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileUsage, Usage: &usage}, nil
				})
			}
			one, err := NewAIReconciliationWorker(repo, aiReconciliationTestConfig(), registry, nil)
			require.NoError(t, err)
			two, err := NewAIReconciliationWorker(repo, aiReconciliationTestConfig(), registry, nil)
			require.NoError(t, err)
			var wg sync.WaitGroup
			results := make(chan error, 2)
			start := make(chan struct{})
			for _, worker := range []*AIReconciliationWorker{one, two} {
				wg.Add(1)
				go func(worker *AIReconciliationWorker) {
					defer wg.Done()
					<-start
					_, err := worker.RunOnce(ctx)
					results <- err
				}(worker)
			}
			close(start)
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			var used, held int64
			var status string
			require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
			require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_reconciliation_jobs WHERE attempt_id=$1`, first.AttemptID).Scan(&status))
			switch scenario {
			case "usage":
				require.Equal(t, int64(5000), used)
				require.Zero(t, held)
				require.Equal(t, "completed", status)
				require.Equal(t, int32(1), lookups.Load())
			case "not_executed":
				require.Zero(t, used)
				require.Zero(t, held)
				require.Equal(t, "completed", status)
				require.Equal(t, int32(1), lookups.Load())
			case "never_sent":
				require.Zero(t, used)
				require.Zero(t, held)
				require.Equal(t, "completed", status)
				require.Zero(t, lookups.Load())
			case "unavailable":
				require.Zero(t, used)
				require.Equal(t, int64(10_000), held)
				require.Equal(t, "manual", status)
			case "lookup_error":
				require.Zero(t, used)
				require.Equal(t, int64(10_000), held)
				require.Equal(t, "pending", status)
				require.Equal(t, int32(1), lookups.Load())
			}
			_, err = one.RunOnce(ctx)
			require.NoError(t, err)
			require.Equal(t, counterBefore+1, metrics.DefaultAIAccounting().CounterValue("reconciliation", "outcome", outcomeLabel), "replaying a terminal reconciliation must not increment its event counter twice")
			if scenario != "never_sent" && scenario != "unavailable" {
				require.Equal(t, int32(1), lookups.Load(), "completed, parked or deferred jobs are not queried repeatedly")
			}
		})
	}
}

func TestAIReconciliationWorkerCancellation(t *testing.T) {
	ctx, db, accounting, org := newAIOperationServiceDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	repo := accounting.store.(*repository.AIReservationRepository)
	first, err := repo.Reserve(ctx, serviceAIOperationRequest(org, "worker-interrupted"))
	require.NoError(t, err)
	_, err = repo.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE ai_usage_reservations SET reconcile_after=created_at WHERE attempt_id=$1`, first.AttemptID)
	require.NoError(t, err)
	stopped, cancel := context.WithCancel(ctx)
	defer cancel()
	registry := map[AIUsageLookupKey]AIUsageLookup{{"nitec", "chat", "test-v1"}: aiUsageLookupFunc(func(ctx context.Context, _ domain.AIReconciliationJob) (domain.AIReconciliationEvidence, error) {
		cancel()
		<-ctx.Done()
		return domain.AIReconciliationEvidence{}, ctx.Err()
	})}
	worker, err := NewAIReconciliationWorker(repo, aiReconciliationTestConfig(), registry, nil)
	require.NoError(t, err)
	_, err = worker.RunOnce(stopped)
	require.Error(t, err)
	var held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&held))
	require.Equal(t, int64(10_000), held)
	_, err = db.Exec(ctx, `UPDATE ai_usage_reconciliation_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE attempt_id=$1`, first.AttemptID)
	require.NoError(t, err)
	registry[AIUsageLookupKey{"nitec", "chat", "test-v1"}] = aiUsageLookupFunc(func(_ context.Context, job domain.AIReconciliationJob) (domain.AIReconciliationEvidence, error) {
		usage := serviceAIUsage(job.RequestedModel)
		return domain.AIReconciliationEvidence{Outcome: domain.AIReconcileUsage, Usage: &usage}, nil
	})
	recovered, err := NewAIReconciliationWorker(repo, aiReconciliationTestConfig(), registry, nil)
	require.NoError(t, err)
	stats, err := recovered.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Completed)
}

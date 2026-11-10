package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func newAIReconciliationTestDB(t *testing.T) (context.Context, *pgxpool.Pool, *AIReservationRepository, uuid.UUID) {
	t.Helper()
	ctx, db, r, _, org, _, _ := newAISettlementTestDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	return ctx, db, r, org
}

func dueAIReconciliation(t *testing.T, ctx context.Context, db *pgxpool.Pool, r *AIReservationRepository, attempt uuid.UUID) {
	t.Helper()
	_, err := db.Exec(ctx, `UPDATE ai_usage_reservations SET reconcile_after=created_at WHERE attempt_id=$1`, attempt)
	require.NoError(t, err)
	_, err = r.ScheduleReconciliation(ctx, time.Minute, 100)
	require.NoError(t, err)
}

func TestAIReconciliationExpiredProcessFence(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(org, &uid, "dead-process", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	dueAIReconciliation(t, ctx, db, r, first.AttemptID)
	a, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, a)
	other, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.Nil(t, other, "a live lease is exclusive")
	// Simulate a process dying without acknowledgement. DB time, not a worker's
	// local clock, determines when a different process can recover its lease.
	_, err = db.Exec(ctx, `UPDATE ai_usage_reconciliation_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE attempt_id=$1`, first.AttemptID)
	require.NoError(t, err)
	b, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, b)
	require.NotEqual(t, a.LeaseToken, b.LeaseToken)
	require.Equal(t, int64(2), b.ClaimNumber)
	usage := aiSettleRequest(org, first.AttemptID, 2, 1)
	evidence := domain.AIReconciliationEvidence{Outcome: domain.AIReconcileUsage, Usage: &usage}
	_, err = r.ApplyReconciliation(ctx, *a, evidence, time.Minute, 10)
	require.ErrorIs(t, err, domain.ErrAIReconciliationLeaseLost, "stale process must not charge after reclaim")
	out, err := r.ApplyReconciliation(ctx, *b, evidence, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, "completed", out.JobStatus)
	_, err = r.ApplyReconciliation(ctx, *b, evidence, time.Minute, 10)
	require.ErrorIs(t, err, domain.ErrAIReconciliationLeaseLost, "lost acknowledgement cannot repeat charge")
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Zero(t, held)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_settlements`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAIReconciliationConcurrentClaims(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	uid := int64(3)
	for i := 0; i < 8; i++ {
		req := aiReserveRequest(org, &uid, uuid.NewString(), 1000)
		out, err := r.Reserve(ctx, req)
		require.NoError(t, err)
		dueAIReconciliation(t, ctx, db, r, out.AttemptID)
	}
	start := make(chan struct{})
	results := make(chan *domain.AIReconciliationJob, 12)
	errors := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			job, err := r.ClaimReconciliation(ctx, time.Minute)
			results <- job
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	seen := make(map[uuid.UUID]bool)
	for job := range results {
		if job != nil {
			require.False(t, seen[job.AttemptID])
			seen[job.AttemptID] = true
		}
	}
	require.Len(t, seen, 8)
}

func TestAIReconciliationAtomicAcknowledgement(t *testing.T) {
	for _, kind := range []domain.AIReconciliationOutcome{domain.AIReconcileUsage, domain.AIReconcileNotDispatched} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, db, r, org := newAIReconciliationTestDB(t)
			uid := int64(3)
			first, err := r.Reserve(ctx, aiReserveRequest(org, &uid, "atomic-ack", 10_000))
			require.NoError(t, err)
			if kind == domain.AIReconcileUsage {
				_, err = r.MarkSent(ctx, org, first.AttemptID)
				require.NoError(t, err)
			}
			dueAIReconciliation(t, ctx, db, r, first.AttemptID)
			job, err := r.ClaimReconciliation(ctx, time.Minute)
			require.NoError(t, err)
			require.NotNil(t, job)
			_, err = db.Exec(ctx, `CREATE FUNCTION reject_reconciliation_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test acknowledgement failure'; END $$; CREATE TRIGGER reject_reconciliation_ack BEFORE UPDATE ON ai_usage_reconciliation_jobs FOR EACH ROW WHEN(NEW.status='completed') EXECUTE FUNCTION reject_reconciliation_ack()`)
			require.NoError(t, err)
			evidence := domain.AIReconciliationEvidence{Outcome: kind}
			if kind == domain.AIReconcileUsage {
				usage := aiSettleRequest(org, first.AttemptID, 2, 1)
				evidence.Usage = &usage
			}
			_, err = r.ApplyReconciliation(ctx, *job, evidence, time.Minute, 10)
			require.Error(t, err)
			var used, held int64
			require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
			require.Zero(t, used)
			require.Equal(t, int64(10_000), held)
			var status string
			require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_reservations WHERE attempt_id=$1`, first.AttemptID).Scan(&status))
			require.Equal(t, "active", status)
			_, err = db.Exec(ctx, `DROP TRIGGER reject_reconciliation_ack ON ai_usage_reconciliation_jobs`)
			require.NoError(t, err)
			out, err := r.ApplyReconciliation(ctx, *job, evidence, time.Minute, 10)
			require.NoError(t, err)
			require.Equal(t, "completed", out.JobStatus)
			_, err = r.ApplyReconciliation(ctx, *job, evidence, time.Minute, 10)
			require.ErrorIs(t, err, domain.ErrAIReconciliationLeaseLost)
			require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
			if kind == domain.AIReconcileUsage {
				require.Equal(t, int64(5000), used)
			} else {
				require.Zero(t, used)
				claimed, err := r.MarkSent(ctx, org, first.AttemptID)
				require.NoError(t, err)
				require.False(t, claimed)
			}
			require.Zero(t, held)
		})
	}
}

func TestAIReconciliationUnknownAndManual(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(org, &uid, "pending-proof", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	dueAIReconciliation(t, ctx, db, r, first.AttemptID)
	for i := 0; i < 2; i++ {
		job, err := r.ClaimReconciliation(ctx, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, job)
		out, err := r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcilePending, Reason: "provider_pending"}, time.Minute, 2)
		require.NoError(t, err)
		if i == 0 {
			require.Equal(t, "pending", out.JobStatus)
		} else {
			require.Equal(t, "manual", out.JobStatus)
			require.Equal(t, "attempts_exhausted", out.Reason)
		}
		pending, err := r.ClaimReconciliation(ctx, time.Minute)
		require.NoError(t, err)
		require.Nil(t, pending, "retry delay and manual status prevent a tight loop")
		_, err = db.Exec(ctx, `UPDATE ai_usage_reconciliation_jobs SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE attempt_id=$1`, first.AttemptID)
		require.NoError(t, err)
	}
	var charged *int64
	var status, reservationStatus string
	require.NoError(t, db.QueryRow(ctx, `SELECT a.charged_microcredits,a.status,r.status FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1`, first.AttemptID).Scan(&charged, &status, &reservationStatus))
	require.Nil(t, charged)
	require.Equal(t, "unknown", status)
	require.Equal(t, "active", reservationStatus)
}

func TestAIReconciliationInvalidAndLateEvidence(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(org, &uid, "changed-dispatch", 10_000))
	require.NoError(t, err)
	dueAIReconciliation(t, ctx, db, r, first.AttemptID)
	job, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, job)
	// Dispatch wins the owner lock while the worker is outside its transaction.
	claimed, err := r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	require.True(t, claimed)
	out, err := r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcileNotDispatched}, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, "pending", out.JobStatus)
	require.Equal(t, "state_changed", out.Reason)
	_, err = db.Exec(ctx, `UPDATE ai_usage_reconciliation_jobs SET next_attempt_at=clock_timestamp() WHERE attempt_id=$1`, first.AttemptID)
	require.NoError(t, err)
	job, err = r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	invalid := aiSettleRequest(org, first.AttemptID, 2, 1)
	invalid.Usage.TotalTokens = aiTokenCount(99)
	out, err = r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcileUsage, Usage: &invalid}, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, "manual", out.JobStatus)
	require.Equal(t, "invalid_evidence", out.Reason)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, first.AttemptID, 2, 1))
	require.NoError(t, err)
	_, err = r.ScheduleReconciliation(ctx, time.Minute, 100)
	require.NoError(t, err)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_reconciliation_jobs WHERE attempt_id=$1`, first.AttemptID).Scan(&status))
	require.Equal(t, "completed", status, "normal completion closes a parked job on the next sweep")
}

func TestAIReconciliationMissingProof(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(org, &uid, "missing-proof", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	dueAIReconciliation(t, ctx, db, r, first.AttemptID)
	job, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	out, err := r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcileNotExecuted}, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, "manual", out.JobStatus)
	require.Equal(t, "invalid_evidence", out.Reason)
	var held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&held))
	require.Equal(t, int64(10_000), held)
}

func TestAIReconciliationMigrationAndDiscovery(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
	require.NoError(t, err)
	parts := strings.Split(string(data), "-- +goose Down")
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err)
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	uid := int64(3)
	for i := 0; i < 2; i++ {
		_, err = r.Reserve(ctx, aiReserveRequest(org, &uid, uuid.NewString(), 1000))
		require.NoError(t, err)
	}
	count, err := r.ScheduleReconciliation(ctx, time.Hour, 1)
	require.NoError(t, err)
	require.Zero(t, count, "fresh calls are not swept")
	_, err = db.Exec(ctx, `UPDATE ai_usage_reservations SET reconcile_after=created_at`)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		count, err = r.ScheduleReconciliation(ctx, time.Hour, 1)
		require.NoError(t, err)
		require.Equal(t, int64(1), count, "existing jobs cannot starve discovery of newer holds")
	}
	_, err = db.Exec(ctx, parts[1])
	require.Error(t, err, "do not erase reconciliation history during rollback")
}

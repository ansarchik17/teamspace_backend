package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func parkAIUnknownForManualReview(t *testing.T, ctx context.Context, db *pgxpool.Pool, r *AIReservationRepository, org uuid.UUID, attempt uuid.UUID) {
	t.Helper()
	_, err := r.MarkSent(ctx, org, attempt)
	require.NoError(t, err)
	_, err = r.FinishFailure(ctx, aiFailureRequest(org, attempt, domain.AIFailureUnknown, "provider_error"))
	require.NoError(t, err)
	dueAIReconciliation(t, ctx, db, r, attempt)
	job, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, job)
	result, err := r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "lookup_unavailable"}, time.Minute, 3)
	require.NoError(t, err)
	require.Equal(t, "manual", result.JobStatus)
}

func TestAIManualReviewRetainsCreditsWithoutBlockingNewGeneration(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	r.userConcurrencyLimit = 1
	user := int64(3)
	request := aiReserveRequest(org, &user, "old-manual", 80_000)
	first, err := r.Reserve(ctx, request)
	require.NoError(t, err)
	parkAIUnknownForManualReview(t, ctx, db, r, org, first.AttemptID)

	replay, err := r.Reserve(ctx, request)
	require.NoError(t, err)
	require.False(t, replay.Created, "manual review never grants a replay another dispatch")
	otherInstance := NewAIReservationRepository(db, nil, WithAIUserConcurrencyLimit(1))
	second, err := otherInstance.Reserve(ctx, aiReserveRequest(org, &user, "new-explicit-request", 10_000))
	require.NoError(t, err, "manual billing review must not hold a generation slot forever")
	require.True(t, second.Created)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "while-new-is-running", 1))
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)

	// With another execution slot available, the old financial hold still
	// prevents overspending. No refund or fabricated zero usage is permitted.
	r.userConcurrencyLimit = 2
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "does-not-fit-budget", 20_001))
	denied := requireReserveDenied(t, err, "RESERVATION_PENDING")
	require.NotEqual(t, "account", denied.Scope)
	var held, used int64
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT r.status,r.reserved_microcredits FROM ai_usage_reservations r WHERE attempt_id=$1`, first.AttemptID).Scan(&status, &held))
	require.Equal(t, "active", status)
	require.EqualValues(t, 80_000, held)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Zero(t, used)
	require.EqualValues(t, 90_000, held)
}

func TestAIManualReviewStillEnforcesConcurrentAdmissionAcrossInstances(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	user := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(org, &user, "manual-before-race", 10_000))
	require.NoError(t, err)
	parkAIUnknownForManualReview(t, ctx, db, r, org, first.AttemptID)
	results := make(chan error, 12)
	for i := range 12 {
		go func(i int) {
			instance := NewAIReservationRepository(db, nil, WithAIUserConcurrencyLimit(1))
			_, err := instance.Reserve(ctx, aiReserveRequest(org, &user, fmt.Sprintf("new-race-%d", i), 1_000))
			results <- err
		}(i)
	}
	accepted := 0
	for range 12 {
		if err := <-results; err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)
		}
	}
	require.Equal(t, 1, accepted)
	var held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&held))
	require.EqualValues(t, 11_000, held)
}

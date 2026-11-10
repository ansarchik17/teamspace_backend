package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIBackgroundUsesSharedBudgetWithoutAllocation(t *testing.T) {
	ctx, db, admin, org, _, now := newAIEnforcementTestDB(t)
	r := NewAIReservationRepository(db, func() time.Time { return *now })
	_, err := r.Reserve(ctx, testReserveReq(org, nil, "unconfigured", 1))
	requireReserveDenied(t, err, "COMPANY_BUDGET_UNCONFIGURED")
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(1_000_000, 100_000, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(800_000, 100_000, 0, 1)))
	// Stored allocations from the preceding release no longer reserve money.
	_, err = db.Exec(ctx, `UPDATE organization_ai_budgets SET service_weekly_quota_microcredits=900000 WHERE organization_id=$1`, org)
	require.NoError(t, err)
	first, err := r.Reserve(ctx, testReserveReq(org, nil, "background", 200))
	require.NoError(t, err)
	require.Nil(t, first.FiveHourPeriodID)
	settlement := testSettleReq(org, first.AttemptID, 100)
	_, err = r.SettleSuccess(ctx, settlement)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, settlement)
	require.NoError(t, err, "replay does not charge twice")
	summary, err := admin.administrationAIBudgetAt(ctx, 1, &org, *now)
	require.NoError(t, err)
	require.Zero(t, summary.ServiceQuota)
	require.False(t, summary.CanSetServiceQuota)
	require.Equal(t, int64(800_000), summary.Allocated)
	require.Equal(t, int64(900_000), summary.Committed)
	require.Equal(t, int64(100_000), summary.Used)
	require.Equal(t, summary.Used, summary.Service.Used)
	unknown, err := r.Reserve(ctx, testReserveReq(org, nil, "background-unknown", 100))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, unknown.AttemptID)
	require.NoError(t, err)
	failure := aiFailureRequest(org, unknown.AttemptID, domain.AIFailureUnknown, "timeout")
	_, err = r.FinishFailure(ctx, failure)
	require.NoError(t, err)
	*now = now.Add(domain.AIWeeklyDuration)
	summary, err = admin.administrationAIBudgetAt(ctx, 1, &org, *now)
	require.NoError(t, err)
	require.Equal(t, int64(100_000), summary.CarriedReserved)
	require.Equal(t, int64(100_000), summary.Service.Reserved)
	_, err = r.Reserve(ctx, testReserveReq(org, nil, "too-much", 901))
	requireReserveDenied(t, err, "RESERVATION_PENDING")
	// Another instance sees the same unknown hold and shares the company lock.
	r2 := NewAIReservationRepository(db, func() time.Time { return *now })
	_, err = r2.Reserve(ctx, testReserveReq(org, nil, "remaining", 900))
	require.NoError(t, err)
	_, err = r.Reserve(ctx, testReserveReq(org, nil, "empty", 1))
	requireReserveDenied(t, err, "RESERVATION_PENDING")
}

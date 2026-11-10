package repository

import (
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
	"time"
)

func TestAIPersonalWeeklyStartsAtFirstAcceptedRequestIndependentlyOfCompany(t *testing.T) {
	ctx, db, r, admin, org, _, now := newAISettlementTestDB(t)
	user := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(2_000_000, 100_000, 10_000, 1)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, user, nil, aiUserQuotaRequest(1_000_000, 100_000, 1, 2)))
	companyStart := *now
	_, err := r.Reserve(ctx, aiReserveRequest(org, nil, "company-service", 10))
	require.NoError(t, err)
	*now = now.Add(48 * time.Hour)
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Nil(t, limits.Weekly.Window, "another payer must not start the employee's week")
	require.Equal(t, 100, *limits.Weekly.RemainingPercent)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "rejected-first-personal", 100_001))
	requireReserveDenied(t, err, "RESERVATION_TOO_LARGE")
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_user_weekly_periods WHERE user_id=$1`, user).Scan(&count))
	require.Zero(t, count)
	*now = now.Add(time.Hour)
	personalStart := *now
	first, err := r.Reserve(ctx, aiReserveRequest(org, &user, "first-personal", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, first.AttemptID, 2, 1))
	require.NoError(t, err)
	limits, err = admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Equal(t, personalStart, limits.Weekly.Window.StartsAt)
	require.Equal(t, personalStart.Add(168*time.Hour), limits.Weekly.Window.EndsAt)
	oldPersonal := *limits.Weekly.PeriodID
	*now = companyStart.Add(168*time.Hour + time.Hour)
	pending, err := r.Reserve(ctx, aiReserveRequest(org, &user, "after-company-rollover", 10_000))
	require.NoError(t, err)
	require.NotEqual(t, first.CompanyPeriodID, pending.CompanyPeriodID)
	limits, err = admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.EqualValues(t, 5_000, limits.Weekly.Used)
	require.Equal(t, oldPersonal, *limits.Weekly.PeriodID)
	*now = personalStart.Add(168 * time.Hour)
	limits, err = admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Nil(t, limits.Weekly.Window)
	require.Zero(t, limits.Weekly.Used)
	require.EqualValues(t, 10_000, limits.Weekly.Reserved, "unknown financial cost is retained")
	*now = now.Add(3 * time.Hour)
	freshStart := *now
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "fresh-personal-week", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, pending.AttemptID)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, pending.AttemptID, 3, 1))
	require.NoError(t, err)
	limits, err = admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Equal(t, freshStart, limits.Weekly.Window.StartsAt)
	require.Equal(t, freshStart.Add(168*time.Hour), limits.Weekly.Window.EndsAt)
	require.Zero(t, limits.Weekly.Used, "late usage belongs to the original personal week")
	var oldUsed int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits FROM ai_user_weekly_periods WHERE id=$1`, oldPersonal).Scan(&oldUsed))
	require.EqualValues(t, 11_000, oldUsed)
}

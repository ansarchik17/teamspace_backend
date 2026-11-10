package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIReserveRemainingBalanceAndReadAPIStayConsistent(t *testing.T) {
	ctx, _, r, admin, org, _, now := newAISettlementTestDB(t)
	user := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(5_000_000_000, 200_000_000, 0, 1)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, user, nil, aiUserQuotaRequest(1_000_000_000, 200_000_000, 1, 2)))
	seed, err := r.Reserve(ctx, aiReserveRequest(org, &user, "seed-expense", 164_180_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, seed.AttemptID)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, seed.AttemptID, 164_180, 0))
	require.NoError(t, err)
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.EqualValues(t, 35_820_000, *limits.FiveHour.Available)
	require.EqualValues(t, 835_820_000, *limits.Weekly.Available)
	require.Equal(t, 17, *limits.FiveHour.RemainingPercent)
	require.True(t, limits.Access.CanStartRequest)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "oversized-context", 900_000_000))
	denied := requireReserveDenied(t, err, "RESERVATION_TOO_LARGE")
	require.Equal(t, "five_hour", denied.Scope)
	require.Nil(t, denied.RecheckAt)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "fits-exactly", 35_820_000))
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "held-balance", 1))
	denied = requireReserveDenied(t, err, "RESERVATION_PENDING")
	require.Equal(t, "five_hour", denied.Scope)
	limits, err = admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.False(t, limits.Access.CanStartRequest)
	require.Equal(t, "unknown", limits.Access.RetryKind)
	require.Equal(t, "RESERVATION_PENDING", limits.Access.Blockers[0].Reason)
}

func TestAICompanyHoldDoesNotBlockPersonalAdmission(t *testing.T) {
	ctx, _, r, admin, org, _, now := newAIReserveTestDB(t)
	user := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(100, 100, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, user, nil, aiUserQuotaRequest(100, 100, 0, 1)))
	_, err := r.Reserve(ctx, aiReserveRequest(org, nil, "service-hold", 100))
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "user-waits", 1))
	require.NoError(t, err)
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.True(t, limits.Access.CanStartRequest)
	require.EqualValues(t, 99, *limits.FiveHour.Available)
	require.Empty(t, limits.Access.Blockers)
}

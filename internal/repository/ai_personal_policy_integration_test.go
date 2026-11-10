package repository

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"sync"
	"testing"
	"time"
)

func TestAIPersonalPolicyDefaultsAndNoCompanyLifecycle(t *testing.T) {
	ctx, db, r, admin, a, _, now := newAISettlementTestDB(t)
	// No company pool or assignment is required for a current member.
	_, err := db.Exec(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=0,max_five_hour_microcredits=0 WHERE organization_id=$1;`, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=NULL,five_hour_limit_microcredits=NULL WHERE user_id=3`)
	require.NoError(t, err)
	for _, uid := range []int64{1, 2, 3, 6} {
		limits, e := admin.GetUserAILimitsWithClockAndMode(ctx, uid, func() time.Time { return *now }, "enforced")
		require.NoError(t, e)
		require.True(t, limits.Access.CanStartRequest, "user %d", uid)
		require.Equal(t, domain.DefaultAIFiveHourMicrocredits, *limits.FiveHour.Limit)
		require.Equal(t, domain.DefaultAIWeeklyMicrocredits, *limits.Weekly.Limit)
		require.Nil(t, limits.Weekly.Window)
	}
	uid := int64(1) // platform administrator, organization_id IS NULL
	req := aiReserveRequest(uuid.Nil, &uid, "personal-without-company", 1_000_000)
	first, err := r.Reserve(ctx, req)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, first.CompanyPeriodID)
	replay, err := r.Reserve(ctx, req)
	require.NoError(t, err)
	require.Equal(t, first.AttemptID, replay.AttemptID)
	require.False(t, replay.Created)
	lookup, err := r.FindOperation(ctx, domain.AIOperationLookup{OrganizationID: uuid.Nil, UserID: &uid, OperationKind: req.OperationKind, IdempotencyKey: req.IdempotencyKey, Fingerprint: req.Fingerprint})
	require.NoError(t, err)
	require.Equal(t, first.AttemptID, lookup.LatestAttempt.AttemptID)
	sent, err := r.MarkSent(ctx, uuid.Nil, first.AttemptID)
	require.NoError(t, err)
	require.True(t, sent)
	settled, err := r.SettleSuccess(ctx, aiSettleRequest(uuid.Nil, first.AttemptID, 78, 150))
	require.NoError(t, err)
	require.EqualValues(t, 528000, settled.ChargedMicrocredits)
	again, err := r.SettleSuccess(ctx, aiSettleRequest(uuid.Nil, first.AttemptID, 78, 150))
	require.NoError(t, err)
	require.False(t, again.Applied)
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, uid, func() time.Time { return *now }, "enforced")
	require.NoError(t, err)
	require.EqualValues(t, 528000, limits.Weekly.Used)
	require.EqualValues(t, 528000, limits.FiveHour.Used)
	require.Zero(t, limits.Weekly.Reserved)
	require.Equal(t, *now, limits.Weekly.Window.StartsAt)
	require.Equal(t, now.Add(168*time.Hour), limits.Weekly.Window.EndsAt)
	// Lowering another unrelated company pool does not affect a member's request.
	member := int64(3)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &member, "member-default", 1_000_000))
	require.NoError(t, err)
	*now = now.Add(169 * time.Hour)
	limits, err = admin.GetUserAILimitsWithClockAndMode(ctx, uid, func() time.Time { return *now }, "enforced")
	require.NoError(t, err)
	require.Nil(t, limits.Weekly.Window)
	require.Equal(t, 100, *limits.Weekly.RemainingPercent)
	_, err = r.Reserve(ctx, aiReserveRequest(uuid.Nil, &uid, "new-personal-week", 1_000_000))
	require.NoError(t, err)
}

func TestAIPersonalPolicyQuotaAuthorizationAndConcurrentAdmission(t *testing.T) {
	ctx, db, r, admin, a, b, _ := newAIReserveTestDB(t)
	// Only the director of this company or the platform admin can change quotas;
	// there is deliberately no company budget configured.
	requireAIQuotaError(t, admin.SetAdministrationAIUserQuota(ctx, 3, 3, nil, aiUserQuotaRequest(100, 20, 0, 0)), apperrors.ErrCodeForbidden)
	requireAIQuotaError(t, admin.SetAdministrationAIUserQuota(ctx, 4, 3, &a, aiUserQuotaRequest(100, 20, 0, 0)), apperrors.ErrCodeForbidden)
	requireAIQuotaError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 5, nil, aiUserQuotaRequest(100, 20, 0, 0)), apperrors.ErrCodeNotFound)
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 20, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 1, 5, &b, aiUserQuotaRequest(100, 20, 0, 0)))
	_, err := admin.PlatformPersonalAIQuota(ctx, 2, 1, nil)
	requireAIQuotaError(t, err, apperrors.ErrCodeForbidden)
	value, err := admin.PlatformPersonalAIQuota(ctx, 1, 1, nil)
	require.NoError(t, err)
	require.Equal(t, domain.DefaultAIWeeklyMicrocredits, value["weekly_quota_microcredits"])
	quota := aiUserQuotaRequest(100, 20, 0, 0)
	_, err = admin.PlatformPersonalAIQuota(ctx, 1, 1, &quota)
	require.NoError(t, err)
	_, err = admin.PlatformPersonalAIQuota(ctx, 1, 1, &quota)
	requireAIQuotaError(t, err, apperrors.ErrCodeAIRevisionConflict)
	var audit int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_ai_personal_quota_audit`).Scan(&audit))
	require.Equal(t, 1, audit)
	uid := int64(1)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"race-one", "race-two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, e := r.Reserve(ctx, aiReserveRequest(uuid.Nil, &uid, key, 15))
			results <- e
		}(key)
	}
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else {
			requireReserveDenied(t, e, "RESERVATION_PENDING")
			denied++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, denied)
	// Wrong declared company is never accepted for a personal account.
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "spoof", 1))
	requireReserveDenied(t, err, "AI_ACCESS_DENIED")
}

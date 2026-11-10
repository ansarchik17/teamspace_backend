package repository

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIFiveHourRestartsOnlyAfterAcceptedRequestAndKeepsOldBilling(t *testing.T) {
	ctx, db, r, admin, org, _, now := newAISettlementTestDB(t)
	user := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(1_000_000, 100_000, 0, 1)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, user, nil, aiUserQuotaRequest(1_000_000, 100_000, 1, 2)))
	start := *now
	first, err := r.Reserve(ctx, aiReserveRequest(org, &user, "old-unknown-hold", 10_000))
	require.NoError(t, err)
	// A real charged result and an unknown hold both belong to the old period.
	paid, err := r.Reserve(ctx, aiReserveRequest(org, &user, "old-paid", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, paid.AttemptID)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, paid.AttemptID, 2, 1))
	require.NoError(t, err)
	*now = start.Add(5*time.Hour - time.Microsecond)
	active, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.NotNil(t, active.FiveHour.Window)
	require.Equal(t, start.Add(5*time.Hour), active.FiveHour.Window.EndsAt)
	*now = start.Add(8*time.Hour + 17*time.Minute)
	var oldPeriodCount int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_ai_five_hour_periods WHERE user_id=$1`, user).Scan(&oldPeriodCount))
	for range 3 {
		idle, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
		require.NoError(t, err)
		require.Nil(t, idle.FiveHour.Window, "no fictional next reset before the first new message")
		require.Nil(t, idle.FiveHour.PeriodID)
		require.Equal(t, 100, *idle.FiveHour.RemainingPercent)
		require.Zero(t, idle.FiveHour.Used)
		require.Zero(t, idle.FiveHour.Reserved)
		require.EqualValues(t, 10_000, idle.Weekly.Reserved, "unknown expense remains financially protected")
		require.EqualValues(t, 5_000, idle.Weekly.Used)
	}
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "rejected-after-idle", 100_001))
	requireReserveDenied(t, err, "RESERVATION_TOO_LARGE")
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_ai_five_hour_periods WHERE user_id=$1`, user).Scan(&count))
	require.Equal(t, oldPeriodCount, count, "GET and rejected messages cannot start the timer")
	*now = now.Add(7 * time.Minute)
	freshStart := *now
	next, err := r.Reserve(ctx, aiReserveRequest(org, &user, "first-new-message", 10_000))
	require.NoError(t, err)
	current, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Equal(t, freshStart, current.FiveHour.Window.StartsAt)
	require.Equal(t, freshStart.Add(5*time.Hour), current.FiveHour.Window.EndsAt)
	require.EqualValues(t, 10_000, current.FiveHour.Reserved)
	require.Zero(t, current.FiveHour.CarriedReserved)
	require.Equal(t, next.FiveHourPeriodID, current.FiveHour.PeriodID)
	// Late usage is recorded against its original window, never the new one.
	_, err = r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, first.AttemptID, 3, 1))
	require.NoError(t, err)
	current, err = admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Zero(t, current.FiveHour.Used)
	require.EqualValues(t, 10_000, current.FiveHour.Reserved)
	require.EqualValues(t, 11_000, current.Weekly.Used)
	// Concurrent callers must share the already accepted window and deadline.
	var wg sync.WaitGroup
	results := make(chan domain.AIReservation, 8)
	failures := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := r.Reserve(ctx, aiReserveRequest(org, &user, fmt.Sprintf("same-window-%d", i), 1))
			results <- res
			failures <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	for result := range results {
		require.Equal(t, next.FiveHourPeriodID, result.FiveHourPeriodID)
	}
	// Real personal-period exhaustion resets at the request-started boundary.
	_, err = r.MarkSent(ctx, org, next.AttemptID)
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(org, next.AttemptID, 100, 0))
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "empty-current-window", 1))
	denied := requireReserveDenied(t, err, "FIVE_HOUR_EXHAUSTED")
	require.Equal(t, freshStart.Add(5*time.Hour), *denied.RecheckAt)
	*now = freshStart.Add(5 * time.Hour)
	expired, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Nil(t, expired.FiveHour.Window)
	require.Equal(t, 100, *expired.FiveHour.RemainingPercent)
	// A rollback cannot rewrite irregular, immutable historical periods.
	migration, err := os.ReadFile("../../db/migrations/20261022100100_ai_five_hour_request_windows.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[1])
	require.Error(t, err)
	require.Contains(t, err.Error(), "request-started history")
}

package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestEvaluateAIAccess_AllIndividualBlockers(t *testing.T) {
	resetTime := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		blocker       domain.AIBlocker
		expectedKind  string
		expectedReset *time.Time
	}{
		{
			name:          "NO_ORGANIZATION",
			blocker:       domain.AIBlocker{Reason: "NO_ORGANIZATION", Scope: "account", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "COMPANY_SUSPENDED",
			blocker:       domain.AIBlocker{Reason: "COMPANY_SUSPENDED", Scope: "company", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "AI_ACCESS_DENIED",
			blocker:       domain.AIBlocker{Reason: "AI_ACCESS_DENIED", Scope: "account", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "COMPANY_BUDGET_UNCONFIGURED",
			blocker:       domain.AIBlocker{Reason: "COMPANY_BUDGET_UNCONFIGURED", Scope: "company", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "COMPANY_BUDGET_DISABLED",
			blocker:       domain.AIBlocker{Reason: "COMPANY_BUDGET_DISABLED", Scope: "company", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "USER_QUOTA_UNCONFIGURED",
			blocker:       domain.AIBlocker{Reason: "USER_QUOTA_UNCONFIGURED", Scope: "account", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "USER_QUOTA_DISABLED",
			blocker:       domain.AIBlocker{Reason: "USER_QUOTA_DISABLED", Scope: "weekly", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "FIVE_HOUR_DISABLED",
			blocker:       domain.AIBlocker{Reason: "FIVE_HOUR_DISABLED", Scope: "five_hour", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "CREDIT_POLICY_UNAVAILABLE",
			blocker:       domain.AIBlocker{Reason: "CREDIT_POLICY_UNAVAILABLE", Scope: "policy", Enforced: true},
			expectedKind:  "requires_action",
			expectedReset: nil,
		},
		{
			name:          "RESERVATION_PENDING",
			blocker:       domain.AIBlocker{Reason: "RESERVATION_PENDING", Scope: "weekly", Enforced: true},
			expectedKind:  "unknown",
			expectedReset: nil,
		},
		{
			name:          "FIVE_HOUR_EXHAUSTED",
			blocker:       domain.AIBlocker{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: true, ResetsAt: &resetTime},
			expectedKind:  "estimated",
			expectedReset: &resetTime,
		},
		{
			name:          "USER_WEEKLY_EXHAUSTED",
			blocker:       domain.AIBlocker{Reason: "USER_WEEKLY_EXHAUSTED", Scope: "weekly", Enforced: true, ResetsAt: &resetTime},
			expectedKind:  "estimated",
			expectedReset: &resetTime,
		},
		{
			name:          "COMPANY_WEEKLY_EXHAUSTED",
			blocker:       domain.AIBlocker{Reason: "COMPANY_WEEKLY_EXHAUSTED", Scope: "company", Enforced: true, ResetsAt: &resetTime},
			expectedKind:  "estimated",
			expectedReset: &resetTime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := evaluateAIAccess([]domain.AIBlocker{tt.blocker}, true)
			require.False(t, access.CanStartRequest)
			require.Equal(t, tt.expectedKind, access.RetryKind)
			if tt.expectedReset == nil {
				require.Nil(t, access.RetryAt)
				require.Nil(t, access.RecheckAt)
			} else {
				require.NotNil(t, access.RetryAt)
				require.Equal(t, *tt.expectedReset, *access.RetryAt)
				require.Equal(t, *tt.expectedReset, *access.RecheckAt)
			}
		})
	}
}

func TestEvaluateAIAccess_WindowBoundaryNotGuaranteeOfAccess(t *testing.T) {
	windowReset := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	// Contract requirement: a window boundary MUST NOT be presented as a guarantee of access removal
	// when a non-timer blocker (unconfigured, disabled, suspended, blocked) is active.
	nonTimerBlockers := []domain.AIBlocker{
		{Reason: "FIVE_HOUR_DISABLED", Scope: "five_hour", Enforced: true},
		{Reason: "USER_QUOTA_DISABLED", Scope: "weekly", Enforced: true},
		{Reason: "COMPANY_BUDGET_DISABLED", Scope: "company", Enforced: true},
		{Reason: "COMPANY_BUDGET_UNCONFIGURED", Scope: "company", Enforced: true},
		{Reason: "USER_QUOTA_UNCONFIGURED", Scope: "account", Enforced: true},
		{Reason: "COMPANY_SUSPENDED", Scope: "company", Enforced: true},
		{Reason: "AI_ACCESS_DENIED", Scope: "account", Enforced: true},
		{Reason: "NO_ORGANIZATION", Scope: "account", Enforced: true},
	}

	for _, nonTimer := range nonTimerBlockers {
		t.Run("combination with "+nonTimer.Reason, func(t *testing.T) {
			blockers := []domain.AIBlocker{
				// Timer blocker has a reset timestamp
				{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: true, ResetsAt: &windowReset},
				nonTimer,
			}
			access := evaluateAIAccess(blockers, true)
			require.False(t, access.CanStartRequest)
			require.Equal(t, "requires_action", access.RetryKind, "requires_action MUST take priority over estimated window reset")
			require.Nil(t, access.RetryAt, "retry_at MUST be null when requires_action applies")
			require.Nil(t, access.RecheckAt, "recheck_at MUST be null when requires_action applies")
		})
	}
}

func TestEvaluateAIAccess_PriorityHierarchy(t *testing.T) {
	windowReset := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	t.Run("requires_action over unknown and estimated", func(t *testing.T) {
		blockers := []domain.AIBlocker{
			{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: true, ResetsAt: &windowReset},
			{Reason: "RESERVATION_PENDING", Scope: "weekly", Enforced: true},
			{Reason: "USER_QUOTA_DISABLED", Scope: "weekly", Enforced: true},
		}
		access := evaluateAIAccess(blockers, true)
		require.Equal(t, "requires_action", access.RetryKind)
		require.Nil(t, access.RetryAt)
	})

	t.Run("unknown over estimated", func(t *testing.T) {
		blockers := []domain.AIBlocker{
			{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: true, ResetsAt: &windowReset},
			{Reason: "RESERVATION_PENDING", Scope: "weekly", Enforced: true},
		}
		access := evaluateAIAccess(blockers, true)
		require.Equal(t, "unknown", access.RetryKind)
		require.Nil(t, access.RetryAt)
	})
}

func TestEvaluateAIAccess_MultipleExhaustedWindowsMaxTimestamp(t *testing.T) {
	t1 := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC)

	t.Run("t2 after t1", func(t *testing.T) {
		blockers := []domain.AIBlocker{
			{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: true, ResetsAt: &t1},
			{Reason: "USER_WEEKLY_EXHAUSTED", Scope: "weekly", Enforced: true, ResetsAt: &t2},
		}
		access := evaluateAIAccess(blockers, true)
		require.Equal(t, "estimated", access.RetryKind)
		require.NotNil(t, access.RetryAt)
		require.Equal(t, t2, *access.RetryAt, "Must take the maximum of all window ends_at")
	})

	t.Run("t1 after t2", func(t *testing.T) {
		blockers := []domain.AIBlocker{
			{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: true, ResetsAt: &t2},
			{Reason: "USER_WEEKLY_EXHAUSTED", Scope: "weekly", Enforced: true, ResetsAt: &t1},
		}
		access := evaluateAIAccess(blockers, true)
		require.Equal(t, "estimated", access.RetryKind)
		require.NotNil(t, access.RetryAt)
		require.Equal(t, t2, *access.RetryAt)
	})
}

func TestEvaluateAIAccess_ObserveMode(t *testing.T) {
	windowReset := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	t.Run("quota blockers in observe mode do not prevent requests", func(t *testing.T) {
		blockers := []domain.AIBlocker{
			{Reason: "USER_WEEKLY_EXHAUSTED", Scope: "weekly", Enforced: false, ResetsAt: &windowReset},
			{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", Enforced: false, ResetsAt: &windowReset},
		}
		access := evaluateAIAccess(blockers, false)
		require.True(t, access.CanStartRequest)
		require.Equal(t, "none", access.RetryKind)
		require.Nil(t, access.RetryAt)
		require.Len(t, access.Blockers, 2)
	})

	t.Run("security and account blockers always enforce even in observe mode", func(t *testing.T) {
		blockers := []domain.AIBlocker{
			{Reason: "USER_WEEKLY_EXHAUSTED", Scope: "weekly", Enforced: false, ResetsAt: &windowReset},
			{Reason: "AI_ACCESS_DENIED", Scope: "account", Enforced: true},
		}
		access := evaluateAIAccess(blockers, false)
		require.False(t, access.CanStartRequest)
		require.Equal(t, "requires_action", access.RetryKind)
		require.Nil(t, access.RetryAt)
	})
}

func TestPeriodsExactBoundaries(t *testing.T) {
	anchor := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// 5-hour periods
	p1, err := domain.AIPeriodAt(anchor, anchor, domain.AIFiveHourDuration)
	require.NoError(t, err)
	require.Equal(t, anchor, p1.StartsAt)
	require.Equal(t, anchor.Add(5*time.Hour), p1.EndsAt)

	// Moment right before the end belongs to p1
	justBefore := p1.EndsAt.Add(-time.Nanosecond)
	p1Check, err := domain.AIPeriodAt(anchor, justBefore, domain.AIFiveHourDuration)
	require.NoError(t, err)
	require.Equal(t, p1.StartsAt, p1Check.StartsAt)
	require.Equal(t, p1.EndsAt, p1Check.EndsAt)

	// Exact moment of p1.EndsAt belongs to p2
	p2, err := domain.AIPeriodAt(anchor, p1.EndsAt, domain.AIFiveHourDuration)
	require.NoError(t, err)
	require.Equal(t, p1.EndsAt, p2.StartsAt)
	require.Equal(t, p1.EndsAt.Add(5*time.Hour), p2.EndsAt)

	// Weekly periods (168 hours)
	w1, err := domain.AIPeriodAt(anchor, anchor, domain.AIWeeklyDuration)
	require.NoError(t, err)
	require.Equal(t, anchor, w1.StartsAt)
	require.Equal(t, anchor.Add(7*24*time.Hour), w1.EndsAt)
	require.Equal(t, 168*time.Hour, w1.EndsAt.Sub(w1.StartsAt))

	w2, err := domain.AIPeriodAt(anchor, w1.EndsAt, domain.AIWeeklyDuration)
	require.NoError(t, err)
	require.Equal(t, w1.EndsAt, w2.StartsAt)
	require.Equal(t, w1.EndsAt.Add(7*24*time.Hour), w2.EndsAt)
}

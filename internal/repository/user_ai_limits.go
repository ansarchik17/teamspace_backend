package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// GetUserAILimits retrieves the authenticated user's own AI limits according to the task 04 contract.
func (r *OrganizationRepository) GetUserAILimits(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
	if !r.aiAccountingEnabled {
		return nil, domain.ErrAIAccountingDisabled
	}
	mode := r.aiEnforcementMode
	if mode == "" {
		mode = domain.AIEnforcementModeEnforced
	}
	return r.getUserAILimits(ctx, userID, time.Now, mode)
}

// GetUserAILimitsWithClockAndMode is used for deterministic testing with a mock clock and enforcement mode.
func (r *OrganizationRepository) GetUserAILimitsWithClockAndMode(ctx context.Context, userID int64, clock func() time.Time, mode string) (*domain.UserAILimits, error) {
	if clock == nil {
		clock = time.Now
	}
	return r.getUserAILimits(ctx, userID, clock, mode)
}

func (r *OrganizationRepository) getUserAILimits(ctx context.Context, userID int64, clock func() time.Time, modeOverride string) (*domain.UserAILimits, error) {
	if userID <= 0 {
		return nil, apperrors.Validation("invalid user id")
	}

	mode := modeOverride
	if mode == "" {
		mode = "enforced"
	}
	if mode != "observe" {
		mode = "enforced"
	}
	isEnforced := (mode == "enforced")

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	serverTime := clock().UTC().Truncate(time.Microsecond)
	policyVersion := domain.AICreditPolicyV1

	// 1. Read user record
	type userInfo struct {
		ID             int64
		OrganizationID *uuid.UUID
		IsActive       bool
		DeletedAt      *time.Time
		IsBlocked      bool
	}
	var u userInfo
	err = tx.QueryRow(ctx, `
		SELECT u.id, u.organization_id, u.is_active, u.deleted_at,
		       EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id = u.id)
		FROM users u
		WHERE u.id = $1
	`, userID).Scan(&u.ID, &u.OrganizationID, &u.IsActive, &u.DeletedAt, &u.IsBlocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("user")
	}
	if err != nil {
		return nil, err
	}

	if u.OrganizationID != nil && r.aiQuotaTestSubjects.Allows(*u.OrganizationID, &userID) {
		mode = domain.AIEnforcementModeObserve
		isEnforced = false
	}

	limits := &domain.UserAILimits{
		ServerTime:          serverTime,
		OrganizationID:      u.OrganizationID,
		EnforcementMode:     mode,
		CreditPolicyVersion: &policyVersion,
		FiveHour:            domain.AIQuotaWindow{State: "unconfigured"},
		Weekly:              domain.AIQuotaWindow{State: "unconfigured"},
		Access: domain.AIAccessState{
			CanStartRequest: true,
			Blockers:        make([]domain.AIBlocker, 0),
			RetryKind:       "none",
		},
	}

	blockers := make([]domain.AIBlocker, 0)

	// 2. Check user access status
	if u.DeletedAt != nil || !u.IsActive || u.IsBlocked {
		blockers = append(blockers, domain.AIBlocker{
			Reason:   "AI_ACCESS_DENIED",
			Scope:    "account",
			Enforced: true,
		})
	}

	// 3. Five-hour window (personal to the user across all companies)
	var fiveWindow domain.AIPeriodWindow
	err = tx.QueryRow(ctx, `SELECT id,starts_at,ends_at,used_microcredits
		FROM user_ai_five_hour_periods WHERE user_id=$1 AND starts_at<=$2 AND ends_at>$2
		ORDER BY starts_at DESC LIMIT 1`, userID, serverTime).Scan(
		&limits.FiveHour.PeriodID, &fiveWindow.StartsAt, &fiveWindow.EndsAt, &limits.FiveHour.Used)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		fiveWindow.StartsAt, fiveWindow.EndsAt = fiveWindow.StartsAt.UTC(), fiveWindow.EndsAt.UTC()
		limits.FiveHour.Window = &fiveWindow
		// Old holds stay in the original personal period and weekly enforcement;
		// they must not reduce a fresh five-hour allowance.
		err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(r.reserved_microcredits),0)::bigint
			FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id
			WHERE a.five_hour_period_id=$1 AND r.status='active'`,
			limits.FiveHour.PeriodID).Scan(&limits.FiveHour.Reserved)
		if err != nil {
			return nil, err
		}
	}

	// Company suspension still disables company accounts; company money does not.
	orgID := uuid.Nil
	if u.OrganizationID != nil {
		orgID = *u.OrganizationID
		var status string
		if err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1`, orgID).Scan(&status); err != nil {
			return nil, err
		}
		if status != "active" {
			blockers = append(blockers, domain.AIBlocker{Reason: "COMPANY_SUSPENDED", Scope: "company", Enforced: true})
		}
	}
	{
		var personalWeek domain.AIPeriodWindow
		err = tx.QueryRow(ctx, `SELECT id,starts_at,ends_at,used_microcredits FROM ai_user_weekly_periods
   WHERE organization_id IS NOT DISTINCT FROM NULLIF($1::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND user_id=$2 AND starts_at<=$3 AND ends_at>$3 ORDER BY starts_at DESC LIMIT 1`, orgID, userID, serverTime).Scan(&limits.Weekly.PeriodID, &personalWeek.StartsAt, &personalWeek.EndsAt, &limits.Weekly.Used)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			personalWeek.StartsAt, personalWeek.EndsAt = personalWeek.StartsAt.UTC(), personalWeek.EndsAt.UTC()
			limits.Weekly.Window = &personalWeek
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE(sum(r.reserved_microcredits),0)::bigint,
   COALESCE(sum(r.reserved_microcredits) FILTER(WHERE $3::uuid IS NULL OR l.period_id IS DISTINCT FROM $3::uuid),0)::bigint
   FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id
   LEFT JOIN ai_user_weekly_attempts l ON l.attempt_id=a.id WHERE o.organization_id IS NOT DISTINCT FROM NULLIF($1::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND o.user_id=$2 AND o.payer_kind='user' AND r.status='active'`, orgID, userID, limits.Weekly.PeriodID).Scan(&limits.Weekly.Reserved, &limits.Weekly.CarriedReserved)
		if err != nil {
			return nil, err
		}
		Q, H, quotaErr := readAIPersonalQuota(ctx, tx, orgID, userID)
		if quotaErr != nil {
			return nil, quotaErr
		}
		effectiveFive := min(H, Q)
		{
			limits.Weekly.Limit = &Q
			limits.FiveHour.Limit = &effectiveFive

			if Q == 0 {
				blockers = append(blockers, domain.AIBlocker{
					Reason:   "USER_QUOTA_DISABLED",
					Scope:    "weekly",
					Enforced: isEnforced,
				})
			}
			if effectiveFive == 0 {
				blockers = append(blockers, domain.AIBlocker{
					Reason:   "FIVE_HOUR_DISABLED",
					Scope:    "five_hour",
					Enforced: isEnforced,
				})
			}

			finishAIQuotaWindow(&limits.Weekly)
			finishAIQuotaWindow(&limits.FiveHour)

			// Check user weekly exhaustion
			if Q > 0 && limits.Weekly.Available != nil && *limits.Weekly.Available == 0 {
				if Q-limits.Weekly.Used > 0 && limits.Weekly.Reserved > 0 {
					blockers = append(blockers, domain.AIBlocker{
						Reason:   "RESERVATION_PENDING",
						Scope:    "weekly",
						Enforced: isEnforced,
					})
				} else {
					blockers = append(blockers, domain.AIBlocker{
						Reason:   "USER_WEEKLY_EXHAUSTED",
						Scope:    "weekly",
						Enforced: isEnforced,
						ResetsAt: aiPersonalWindowEnd(limits.Weekly.Window),
					})
				}
			}

			// Check user five-hour exhaustion
			if effectiveFive > 0 && limits.FiveHour.Available != nil && *limits.FiveHour.Available == 0 {
				var fiveEndsAt *time.Time
				if limits.FiveHour.Window != nil {
					fiveEndsAt = &limits.FiveHour.Window.EndsAt
				}
				if effectiveFive-limits.FiveHour.Used > 0 && limits.FiveHour.Reserved > 0 {
					blockers = append(blockers, domain.AIBlocker{
						Reason:   "RESERVATION_PENDING",
						Scope:    "five_hour",
						Enforced: isEnforced,
					})
				} else {
					blockers = append(blockers, domain.AIBlocker{
						Reason:   "FIVE_HOUR_EXHAUSTED",
						Scope:    "five_hour",
						Enforced: isEnforced,
						ResetsAt: fiveEndsAt,
					})
				}
			}
		}
	}

	// 6. Build Access evaluation
	limits.Access = evaluateAIAccess(blockers, isEnforced)
	return limits, nil
}

func evaluateAIAccess(blockers []domain.AIBlocker, isEnforced bool) domain.AIAccessState {
	access := domain.AIAccessState{
		Blockers: blockers,
	}

	hasEnforcedBlocker := false
	for _, b := range blockers {
		if b.Enforced {
			hasEnforcedBlocker = true
			break
		}
	}

	if !hasEnforcedBlocker {
		access.CanStartRequest = true
		access.RetryKind = "none"
		access.RetryAt = nil
		access.RecheckAt = nil
		return access
	}

	access.CanStartRequest = false

	// Priority: requires_action > unknown > estimated
	hasRequiresAction := false
	hasUnknown := false
	var latestReset *time.Time

	for _, b := range blockers {
		if !b.Enforced {
			continue
		}
		switch b.Reason {
		case "NO_ORGANIZATION", "COMPANY_SUSPENDED", "AI_ACCESS_DENIED",
			"COMPANY_BUDGET_UNCONFIGURED", "COMPANY_BUDGET_DISABLED",
			"USER_QUOTA_UNCONFIGURED", "USER_QUOTA_DISABLED", "FIVE_HOUR_DISABLED",
			"CREDIT_POLICY_UNAVAILABLE":
			hasRequiresAction = true
		case "RESERVATION_PENDING":
			hasUnknown = true
		case "COMPANY_WEEKLY_EXHAUSTED", "USER_WEEKLY_EXHAUSTED", "FIVE_HOUR_EXHAUSTED":
			if b.ResetsAt != nil {
				if latestReset == nil || b.ResetsAt.After(*latestReset) {
					t := *b.ResetsAt
					latestReset = &t
				}
			}
		default:
			hasRequiresAction = true
		}
	}

	if hasRequiresAction {
		access.RetryKind = "requires_action"
		access.RetryAt = nil
		access.RecheckAt = nil
	} else if hasUnknown {
		access.RetryKind = "unknown"
		access.RetryAt = nil
		access.RecheckAt = nil
	} else if latestReset != nil {
		access.RetryKind = "estimated"
		access.RetryAt = latestReset
		access.RecheckAt = latestReset
	} else {
		access.RetryKind = "requires_action"
		access.RetryAt = nil
		access.RecheckAt = nil
	}

	return access
}

func aiPersonalWindowEnd(w *domain.AIPeriodWindow) *time.Time {
	if w == nil {
		return nil
	}
	return &w.EndsAt
}

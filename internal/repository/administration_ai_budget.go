package repository

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) AdministrationAIBudget(ctx context.Context, actor int64, org *uuid.UUID) (*domain.AIBudgetSummary, error) {
	return r.readAdministrationAIBudget(ctx, actor, org, time.Now)
}

func (r *OrganizationRepository) administrationAIBudgetAt(ctx context.Context, actor int64, org *uuid.UUID, now time.Time) (*domain.AIBudgetSummary, error) {
	return r.readAdministrationAIBudget(ctx, actor, org, func() time.Time { return now })
}

func (r *OrganizationRepository) readAdministrationAIBudget(ctx context.Context, actor int64, org *uuid.UUID, clock func() time.Time) (*domain.AIBudgetSummary, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger}
	scope, err := inner.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out, err := inner.aiBudgetSummary(ctx, scope.ID, org != nil, clock())
	if err != nil {
		return nil, err
	}
	out.EnforcementMode = r.aiEnforcementMode
	if out.EnforcementMode == "" {
		out.EnforcementMode = domain.AIEnforcementModeEnforced
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// Caller supplies the transaction so reads and administrative writes use the
// same accounting formula without opening a nested read-only transaction.
func (r *OrganizationRepository) aiBudgetSummary(ctx context.Context, organizationID uuid.UUID, platform bool, now time.Time) (*domain.AIBudgetSummary, error) {
	// Capture after the read snapshot is established: a concurrently committed
	// first reservation must not expose an anchor newer than the observed time.
	out := &domain.AIBudgetSummary{OrganizationID: organizationID, ServerTime: now.UTC().Truncate(time.Microsecond)}
	var status string
	err := r.db.QueryRow(ctx, `SELECT o.status,b.weekly_budget_microcredits,b.max_five_hour_microcredits,COALESCE(b.revision,0) FROM organizations o LEFT JOIN organization_ai_budgets b ON b.organization_id=o.id WHERE o.id=$1`, organizationID).Scan(&status, &out.WeeklyBudget, &out.MaxFiveHour, &out.Revision)
	if err != nil {
		return nil, err
	}
	out.Configured = out.WeeklyBudget != nil
	out.CanSetCompanyBudget = platform
	out.CanSetUserQuotas = status == "active"
	var anchor time.Time
	err = r.db.QueryRow(ctx, `SELECT anchor_at FROM organization_ai_weekly_anchors WHERE organization_id=$1`, organizationID).Scan(&anchor)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var start, end *time.Time
	if err == nil {
		window, windowErr := domain.AIPeriodAt(anchor, out.ServerTime, domain.AIWeeklyDuration)
		if windowErr != nil {
			return nil, windowErr
		}
		out.Window = &window
		start = &window.StartsAt
		end = &window.EndsAt
		err = r.db.QueryRow(ctx, `SELECT id,used_microcredits FROM organization_ai_weekly_periods WHERE organization_id=$1 AND starts_at=$2`, organizationID, start).Scan(&out.PeriodID, &out.Used)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	// Live reservation rows are authoritative for holds, including old windows
	// not yet materialized into a current period's carried counters. Never add
	// mirrored period.reserved counters to these rows a second time.
	const summarySQL = `WITH period AS (
 SELECT id FROM organization_ai_weekly_periods WHERE organization_id=$1 AND starts_at=$2
), holds AS (
 SELECT o.payer_kind,o.user_id,
 SUM(r.reserved_microcredits)::numeric reserved,
 SUM(CASE WHEN $2::timestamptz IS NULL OR p.starts_at<$2 THEN r.reserved_microcredits ELSE 0 END)::numeric carried
 FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id
 JOIN ai_usage_operations o ON o.id=a.operation_id
 JOIN organization_ai_weekly_periods p ON p.id=a.company_period_id
 WHERE o.organization_id=$1 AND r.status='active' AND ($3::timestamptz IS NULL OR p.starts_at<$3)
 GROUP BY o.payer_kind,o.user_id
), quotas AS (
 SELECT q.user_id,CASE WHEN u.organization_id=$1 AND u.deleted_at IS NULL THEN COALESCE(q.weekly_quota_microcredits,0) ELSE 0 END::numeric quota
 FROM organization_ai_user_quotas q JOIN users u ON u.id=q.user_id WHERE q.organization_id=$1
), usage AS (
 SELECT u.user_id,u.used_microcredits::numeric used FROM organization_ai_user_weekly_usage u JOIN period p ON p.id=u.period_id WHERE u.organization_id=$1
), ids AS (SELECT user_id FROM quotas UNION SELECT user_id FROM usage UNION SELECT user_id FROM holds WHERE payer_kind='user'),
 employees AS (
 SELECT COALESCE(q.quota,0) quota,COALESCE(u.used,0) used,COALESCE(h.reserved,0) reserved
 FROM ids LEFT JOIN quotas q USING(user_id) LEFT JOIN usage u USING(user_id) LEFT JOIN holds h ON h.user_id=ids.user_id AND h.payer_kind='user'
), service AS (
 SELECT COALESCE((SELECT used_microcredits FROM organization_ai_service_weekly_usage WHERE organization_id=$1 AND period_id=(SELECT id FROM period)),0)::numeric used,
 COALESCE((SELECT SUM(reserved) FROM holds WHERE payer_kind='service'),0) reserved,
 COALESCE((SELECT SUM(carried) FROM holds WHERE payer_kind='service'),0) carried
)
SELECT COALESCE((SELECT SUM(quota) FROM employees),0)::bigint,
 (COALESCE((SELECT SUM(GREATEST(quota,used+reserved)) FROM employees),0)+s.used+s.reserved)::bigint,
 COALESCE((SELECT SUM(reserved) FROM holds),0)::bigint, COALESCE((SELECT SUM(carried) FROM holds),0)::bigint,
 s.used::bigint,s.reserved::bigint,s.carried::bigint FROM service s`
	err = r.db.QueryRow(ctx, summarySQL, organizationID, start, end).Scan(&out.EmployeeAllocated, &out.Committed, &out.Reserved, &out.CarriedReserved, &out.Service.Used, &out.Service.Reserved, &out.Service.CarriedReserved)
	if err != nil {
		return nil, err
	}
	out.Allocated = out.EmployeeAllocated
	if out.WeeklyBudget != nil {
		available := aiBudgetRemaining(*out.WeeklyBudget, out.Used, out.Reserved)
		unallocated := aiBudgetRemaining(*out.WeeklyBudget, out.Committed, 0)
		over := aiBudgetRemaining(out.Committed, *out.WeeklyBudget, 0)
		out.Available = &available
		out.Service.Available = available // Shared funds, not a separate allocation.
		out.Unallocated = &unallocated
		out.Overcommitted = &over
		if *out.WeeklyBudget > 0 {
			// Percentage multiplication must not overflow even at int64 limits.
			percent := int(new(big.Int).Quo(new(big.Int).Mul(big.NewInt(available), big.NewInt(100)), big.NewInt(*out.WeeklyBudget)).Int64())
			out.RemainingPercent = &percent
		}
	}
	return out, nil
}

func aiBudgetRemaining(limit, used, reserved int64) int64 {
	if used >= limit {
		return 0
	}
	remaining := limit - used
	if reserved >= remaining {
		return 0
	}
	return remaining - reserved
}

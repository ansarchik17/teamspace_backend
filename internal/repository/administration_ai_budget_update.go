package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) SetAdministrationAIBudget(ctx context.Context, actor int64, org *uuid.UUID, req domain.SetAIBudgetRequest) error {
	if !req.Valid() {
		return apperrors.Validation("invalid AI budget settings")
	}
	if org == nil {
		return apperrors.Forbidden("platform administrator required")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize first-time creation too, when no budget row exists yet.
	var owner *int64
	err = tx.QueryRow(ctx, `SELECT owner_id FROM organizations WHERE id=$1 FOR UPDATE`, *org).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrOrganizationNotFound
	}
	if err != nil {
		return err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, owner, true); err != nil {
		return err
	}
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `SELECT organization_id FROM organization_ai_budgets WHERE organization_id=$1 FOR UPDATE`, *org).Scan(&locked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger}
	before, err := inner.aiBudgetSummary(ctx, *org, true, time.Now())
	if err != nil {
		return err
	}
	if before.Revision != *req.Revision {
		return apperrors.New(apperrors.ErrCodeAIRevisionConflict, "AI budget revision changed", 409).WithDetails(map[string]any{"scope": "company", "expected_revision": strconv.FormatInt(*req.Revision, 10), "current_revision": strconv.FormatInt(before.Revision, 10)})
	}
	if before.Configured && *before.WeeklyBudget == *req.WeeklyBudget && *before.MaxFiveHour == *req.MaxFiveHour {
		return tx.Commit(ctx)
	}
	// No advance background allocation; actual usage and holds remain committed.
	committed := before.Committed
	budget := *req.WeeklyBudget
	if budget != 0 && (budget < committed || budget < before.Used || before.Reserved > budget-before.Used) {
		return aiBudgetConflict("BUDGET_BELOW_COMMITTED")
	}
	beforeJSON, err := aiBudgetSettingsSnapshot(ctx, tx, *org)
	if err != nil {
		return err
	}
	if !before.Configured {
		_, err = tx.Exec(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits,service_weekly_quota_microcredits) VALUES($1,$2,$3,0)`, *org, budget, *req.MaxFiveHour)
	} else {
		_, err = tx.Exec(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=$2,max_five_hour_microcredits=$3,service_weekly_quota_microcredits=0 WHERE organization_id=$1`, *org, budget, *req.MaxFiveHour)
	}
	if err != nil {
		return err
	}
	afterJSON, err := aiBudgetSettingsSnapshot(ctx, tx, *org)
	if err != nil {
		return err
	}
	if err = writeAdministrationAudit(ctx, tx, *org, actor, "ai.budget.update", "ai_budget", org.String(), beforeJSON, afterJSON); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func aiBudgetConflict(reason string) error {
	return apperrors.New(apperrors.ErrCodeAIBudgetConflict, "AI budget is below committed obligations", 409).WithDetail("reason", reason)
}

func aiBudgetSettingsSnapshot(ctx context.Context, tx pgx.Tx, org uuid.UUID) (json.RawMessage, error) {
	var snapshot json.RawMessage
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('weekly_budget_microcredits',weekly_budget_microcredits::text,'max_five_hour_microcredits',max_five_hour_microcredits::text,'service_weekly_quota_microcredits',service_weekly_quota_microcredits::text,'revision',revision::text) FROM organization_ai_budgets WHERE organization_id=$1`, org).Scan(&snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return json.RawMessage(`null`), nil
	}
	return snapshot, err
}

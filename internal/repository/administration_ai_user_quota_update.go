package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) SetAdministrationAIUserQuota(ctx context.Context, actor, target int64, org *uuid.UUID, req domain.SetAIUserQuotaRequest) error {
	if target <= 0 || !req.Valid() {
		return apperrors.Validation("invalid AI user quota")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger}
	scope, err := inner.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	var owner *int64
	var status string
	if err = tx.QueryRow(ctx, `SELECT owner_id,status FROM organizations WHERE id=$1 FOR UPDATE`, scope.ID).Scan(&owner, &status); err != nil {
		return err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, owner, org != nil); err != nil {
		return err
	}
	if status != "active" {
		return apperrors.New(apperrors.ErrCodeAICompanySuspended, "AI company is suspended", 409).WithDetail("reason", "COMPANY_SUSPENDED")
	}
	// Administration locks the organization before membership, matching employee
	// blocking. Membership still precedes the budget, matching period opening and
	// lifecycle triggers. SHARE also permits the director's actor authorization.
	var member int64
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR SHARE`, target, scope.ID).Scan(&member)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NotFound("employee")
	}
	if err != nil {
		return err
	}
	// Company revision remains in the request for older clients; personal
	// quota writes no longer depend on company allocation or company revision.

	var oldQ, oldH *int64
	var revision int64
	err = tx.QueryRow(ctx, `SELECT weekly_quota_microcredits,five_hour_limit_microcredits,revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, scope.ID, target).Scan(&oldQ, &oldH, &revision)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if revision != *req.Revision {
		return aiQuotaRevisionConflict("user", *req.Revision, revision)
	}
	if oldQ != nil && oldH != nil && *oldQ == *req.WeeklyQuota && *oldH == *req.FiveHourLimit {
		return tx.Commit(ctx)
	}
	if *req.FiveHourLimit > *req.WeeklyQuota {
		return apperrors.Validation("invalid AI five hour quota").WithDetail("five_hour_limit_microcredits", "max")
	}

	before, err := aiUserQuotaSnapshot(ctx, tx, scope.ID, target)
	if err != nil {
		return err
	}
	if exists {
		_, err = tx.Exec(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=$3,five_hour_limit_microcredits=$4 WHERE organization_id=$1 AND user_id=$2`, scope.ID, target, *req.WeeklyQuota, *req.FiveHourLimit)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,$2,$3,$4)`, scope.ID, target, *req.WeeklyQuota, *req.FiveHourLimit)
	}
	if err != nil {
		return err
	}
	after, err := aiUserQuotaSnapshot(ctx, tx, scope.ID, target)
	if err != nil {
		return err
	}
	if err = writeAdministrationAudit(ctx, tx, scope.ID, actor, "ai.user_quota.update", "ai_user_quota", strconv.FormatInt(target, 10), before, after); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func aiQuotaRevisionConflict(scope string, expected, current int64) error {
	return apperrors.New(apperrors.ErrCodeAIRevisionConflict, "AI quota revision changed", 409).WithDetails(map[string]any{"scope": scope, "expected_revision": strconv.FormatInt(expected, 10), "current_revision": strconv.FormatInt(current, 10)})
}

func aiUserQuotaSnapshot(ctx context.Context, tx pgx.Tx, org uuid.UUID, user int64) (json.RawMessage, error) {
	var snapshot json.RawMessage
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('weekly_quota_microcredits',weekly_quota_microcredits::text,'five_hour_limit_microcredits',five_hour_limit_microcredits::text,'revision',revision::text) FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2`, org, user).Scan(&snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return json.RawMessage(`null`), nil
	}
	return snapshot, err
}

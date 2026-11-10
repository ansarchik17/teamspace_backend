package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// PlatformPersonalAIQuota manages accounts without a company. Company members
// continue using the tenant-scoped API, including directors' ownership checks.
func (r *OrganizationRepository) PlatformPersonalAIQuota(ctx context.Context, actor, target int64, req *domain.SetAIUserQuotaRequest) (map[string]any, error) {
	if target <= 0 || (req != nil && (!req.Valid() || *req.FiveHourLimit > *req.WeeklyQuota)) {
		return nil, apperrors.Validation("invalid AI user quota")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = authorizeAdministration(ctx, tx, actor, nil, true); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE`, []int64{actor, target}); err != nil {
		return nil, err
	}
	// Lock targets in the same order as personal reservations. Authorize against
	// the live role, never the JWT or a user-supplied organization.
	var org *uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT organization_id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, target).Scan(&org); errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("user")
	} else if err != nil {
		return nil, err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, nil, true); err != nil {
		return nil, err
	}
	if org != nil {
		return nil, apperrors.Validation("use organization AI quota API for company members")
	}
	q, h, err := readAIPersonalQuota(ctx, tx, uuid.Nil, target)
	if err != nil {
		return nil, err
	}
	var rev int64
	err = tx.QueryRow(ctx, `SELECT revision FROM user_ai_personal_quotas WHERE user_id=$1`, target).Scan(&rev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if req != nil {
		if *req.Revision != rev {
			return nil, aiQuotaRevisionConflict("user", *req.Revision, rev)
		}
		before, _ := json.Marshal(map[string]int64{"weekly_quota_microcredits": q, "five_hour_limit_microcredits": h, "revision": rev})
		err = tx.QueryRow(ctx, `INSERT INTO user_ai_personal_quotas(user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,$2,$3) ON CONFLICT(user_id) DO UPDATE SET weekly_quota_microcredits=$2,five_hour_limit_microcredits=$3,revision=user_ai_personal_quotas.revision+1,updated_at=now() RETURNING revision`, target, *req.WeeklyQuota, *req.FiveHourLimit).Scan(&rev)
		if err != nil {
			return nil, err
		}
		q, h = *req.WeeklyQuota, *req.FiveHourLimit
		after, _ := json.Marshal(map[string]int64{"weekly_quota_microcredits": q, "five_hour_limit_microcredits": h, "revision": rev})
		if _, err = tx.Exec(ctx, `INSERT INTO user_ai_personal_quota_audit(user_id,actor_id,before_value,after_value) VALUES($1,$2,$3,$4)`, target, actor, before, after); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"user_id": target, "weekly_quota_microcredits": q, "five_hour_limit_microcredits": h, "revision": rev}, nil
}

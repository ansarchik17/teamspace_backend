package repository

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Empty company identity is SQL NULL, never a fabricated organization.
func nullableAIUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func readAIPersonalQuota(ctx context.Context, tx pgx.Tx, org uuid.UUID, user int64) (int64, int64, error) {
	q, h := domain.DefaultAIWeeklyMicrocredits, domain.DefaultAIFiveHourMicrocredits
	var weekly, five *int64
	var err error
	if org != uuid.Nil {
		err = tx.QueryRow(ctx, `SELECT weekly_quota_microcredits,five_hour_limit_microcredits FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2`, org, user).Scan(&weekly, &five)
	} else {
		err = tx.QueryRow(ctx, `SELECT weekly_quota_microcredits,five_hour_limit_microcredits FROM user_ai_personal_quotas WHERE user_id=$1`, user).Scan(&weekly, &five)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, err
	}
	if weekly != nil {
		q = *weekly
	}
	if five != nil {
		h = *five
	}
	return q, h, nil
}

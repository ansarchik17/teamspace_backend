package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIQuotaDefaultsMigrationResetsOverridesAndPreservesAccounting(t *testing.T) {
	ctx, db, reservations, admin, org, _, _ := newAISettlementTestDB(t)
	user := int64(3)
	paid, err := reservations.Reserve(ctx, aiReserveRequest(org, &user, "before-policy-paid", 10000))
	require.NoError(t, err)
	_, err = reservations.SettleSuccess(ctx, aiSettleRequest(org, paid.AttemptID, 1, 1))
	require.NoError(t, err)
	_, err = reservations.Reserve(ctx, aiReserveRequest(org, &user, "before-policy-held", 20000))
	require.NoError(t, err)
	before, err := admin.GetUserAILimits(ctx, user)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=0,five_hour_limit_microcredits=0 WHERE user_id=3;
 INSERT INTO user_ai_personal_quotas(user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES(1,123456,789);`)
	require.NoError(t, err)
	// Current member with no assigned quota; former membership must stay released.
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id) VALUES($1,2),($1,6)`, org)
	require.NoError(t, err)
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_ai_user_quotas WHERE user_id=3`).Scan(&revision))
	migration, err := os.ReadFile("../../db/migrations/20261027000100_ai_personal_quota_defaults.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	require.EqualValues(t, 100000000000, domain.DefaultAIWeeklyMicrocredits)
	require.EqualValues(t, 20000000000, domain.DefaultAIFiveHourMicrocredits)
	for _, uid := range []int64{1, 2, 3, 6} {
		limits, err := admin.GetUserAILimits(ctx, uid)
		require.NoError(t, err)
		require.Equal(t, domain.DefaultAIWeeklyMicrocredits, *limits.Weekly.Limit)
		require.Equal(t, domain.DefaultAIFiveHourMicrocredits, *limits.FiveHour.Limit)
	}
	after, err := admin.GetUserAILimits(ctx, user)
	require.NoError(t, err)
	require.Equal(t, before.Weekly.Used, after.Weekly.Used)
	require.Equal(t, before.Weekly.Reserved, after.Weekly.Reserved)
	require.Equal(t, before.Weekly.Window, after.Weekly.Window)
	require.Equal(t, before.FiveHour.Used, after.FiveHour.Used)
	require.Equal(t, before.FiveHour.Reserved, after.FiveHour.Reserved)
	require.Equal(t, before.FiveHour.Window, after.FiveHour.Window)
	var currentRevision, auditCount int64
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_ai_user_quotas WHERE user_id=3`).Scan(&currentRevision))
	require.Equal(t, revision+1, currentRevision, "old admin forms must fail revision validation")
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_personal_quota_policy_resets WHERE before_value->'revision' <> after_value->'revision'`).Scan(&auditCount))
	require.EqualValues(t, 3, auditCount)
	var released *int64
	require.NoError(t, db.QueryRow(ctx, `SELECT weekly_quota_microcredits FROM organization_ai_user_quotas WHERE user_id=6`).Scan(&released))
	require.Nil(t, released, "do not restore a former company's assignment")
}

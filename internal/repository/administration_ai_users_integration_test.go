package repository

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestAdministrationAIUsers(t *testing.T) {
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	_, err := db.Exec(ctx, `ALTER TABLE users ADD COLUMN email TEXT;`)
	require.NoError(t, err)
	for _, file := range []string{"20261012000200_company_user_blocks.sql", "20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql", "20261022100200_ai_personal_weekly_windows.sql", "20261020100900_ai_enforcement_mode_unconfigured.sql", "20261022100400_ai_personal_access_policy.sql", "20261022100500_ai_final_request_cutoffs.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, e)
	}
	r := NewOrganizationRepository(db, zap.NewNop())
	now := time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	filter := domain.AdministrationUserFilter{Limit: 25}
	page, err := r.administrationAIUsers(ctx, 2, nil, filter, clock)
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	require.True(t, page.Items[0].Configured)
	require.Equal(t, "active", page.Items[0].Weekly.State)
	require.Nil(t, page.Window)
	_, err = r.administrationAIUsers(ctx, 3, nil, filter, clock)
	require.Error(t, err)
	_, err = r.administrationAIUsers(ctx, 2, &b, filter, clock)
	require.Error(t, err)
	other, err := r.administrationAIUsers(ctx, 1, &b, filter, clock)
	require.NoError(t, err)
	require.Equal(t, int64(4), other.Items[0].UserID)
	require.Equal(t, int64(5), other.Items[1].UserID)
	for _, bad := range []domain.AdministrationUserFilter{{Limit: 0}, {Limit: 101}, {Limit: 1, Offset: -1}, {Limit: 1, Offset: 2147483648}, {Limit: 1, Query: strings.Repeat("я", 256)}} {
		_, err = r.administrationAIUsers(ctx, 2, nil, bad, clock)
		require.Error(t, err)
	}
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id,username,email) VALUES(7,$1,'new','new@example.com'),(8,$1,'deleted','deleted@example.com'),(9,$1,'moved','moved@example.com')`, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now() WHERE id=8`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=9`, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET full_name_local='Иван 100%_готов',email='ivan@example.com' WHERE id=3`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_user_blocks(user_id,organization_id,actor_id) VALUES(3,$1,2)`, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=3`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,1000,100),($2,1000,100)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,2,0,0),($1,3,500,100)`, a)
	require.NoError(t, err)
	weekStart := now.Add(-time.Hour)
	weekAnchor := weekStart.Add(-168 * time.Hour)
	fiveAnchor := now.Add(-200 * time.Hour)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_weekly_anchors VALUES($1,$3),($2,$3)`, a, b, weekAnchor)
	require.NoError(t, err)
	wCurrent, wOld, wOther, fCurrent, fOld := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_weekly_periods(id,organization_id,anchor_at,starts_at,ends_at) VALUES($1,$2,$3,$4,$5),($6,$2,$3,$3,$4),($7,$8,$3,$3,$4)`, wCurrent, a, weekAnchor, weekStart, weekStart.Add(168*time.Hour), wOld, wOther, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO user_ai_five_hour_anchors VALUES(3,$1)`, fiveAnchor)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO user_ai_five_hour_periods(id,user_id,anchor_at,starts_at,ends_at,used_microcredits) VALUES($1,3,$2,$3,$4,20),($5,3,$2,$6,$7,900)`, fCurrent, fiveAnchor, now, now.Add(5*time.Hour), fOld, now.Add(-170*time.Hour), now.Add(-165*time.Hour))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_user_weekly_usage(organization_id,period_id,user_id,used_microcredits) VALUES($1,$2,3,120),($1,$3,3,900)`, a, wCurrent, wOld)
	require.NoError(t, err)
	personalWeek := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO ai_user_weekly_periods(id,organization_id,user_id,starts_at,ends_at,used_microcredits) VALUES($1,$2,3,$3,$4,120)`, personalWeek, a, weekStart, weekStart.Add(168*time.Hour))
	require.NoError(t, err)
	reserve := func(org, week, five uuid.UUID, amount int64) {
		t.Helper()
		op, attempt := uuid.New(), uuid.New()
		_, e := db.Exec(ctx, `INSERT INTO ai_usage_operations(id,organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,$2,3,'user','test',$3,decode(repeat('ab',32),'hex'))`, op, org, op.String())
		require.NoError(t, e)
		_, e = db.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,company_period_id,five_hour_period_id) VALUES($1,$2,$3,1,'nitec','chat','model','credits-v1','test',1000,$4,$5)`, attempt, op, org, week, five)
		require.NoError(t, e)
		_, e = db.Exec(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,$2)`, attempt, amount)
		require.NoError(t, e)
		if org == a && week == wCurrent {
			_, e = db.Exec(ctx, `INSERT INTO ai_user_weekly_attempts(attempt_id,period_id) VALUES($1,$2)`, attempt, personalWeek)
			require.NoError(t, e)
		}
	}
	reserve(a, wCurrent, fCurrent, 10)
	reserve(a, wOld, fOld, 20)
	reserve(b, wOther, fOld, 15)
	page, err = r.administrationAIUsers(ctx, 2, nil, filter, clock)
	require.NoError(t, err)
	require.Equal(t, 3, page.Total)
	require.Len(t, page.Items, 3)
	require.Equal(t, int64(2), page.Items[0].UserID)
	require.True(t, page.Items[0].IsDirector)
	require.Equal(t, "disabled", page.Items[0].Weekly.State)
	blocked := page.Items[1]
	require.Equal(t, int64(3), blocked.UserID)
	require.True(t, blocked.IsBlocked)
	require.False(t, blocked.IsActive)
	require.True(t, blocked.Configured)
	require.Equal(t, int64(120), blocked.Weekly.Used)
	require.Equal(t, int64(30), blocked.Weekly.Reserved)
	require.Equal(t, int64(20), blocked.Weekly.CarriedReserved)
	require.Equal(t, int64(350), *blocked.Weekly.Available)
	require.Equal(t, 70, *blocked.Weekly.RemainingPercent)
	require.Equal(t, int64(20), blocked.FiveHour.Used)
	require.Equal(t, int64(10), blocked.FiveHour.Reserved)
	require.Zero(t, blocked.FiveHour.CarriedReserved)
	require.Equal(t, int64(70), *blocked.FiveHour.Available)
	require.Equal(t, int64(500), blocked.Committed)
	require.True(t, page.Items[2].Configured)
	for offset, id := range []int64{2, 3, 7} {
		p, e := r.administrationAIUsers(ctx, 2, nil, domain.AdministrationUserFilter{Limit: 1, Offset: offset}, clock)
		require.NoError(t, e)
		require.Equal(t, 3, p.Total)
		require.Len(t, p.Items, 1)
		require.Equal(t, id, p.Items[0].UserID)
	}
	p, err := r.administrationAIUsers(ctx, 2, nil, domain.AdministrationUserFilter{Limit: 100, Offset: 100}, clock)
	require.NoError(t, err)
	require.Empty(t, p.Items)
	require.Equal(t, 3, p.Total)
	for _, q := range []string{"  Иван  ", "%_", "IVAN@EXAMPLE.COM"} {
		p, e := r.administrationAIUsers(ctx, 2, nil, domain.AdministrationUserFilter{Limit: 25, Query: q}, clock)
		require.NoError(t, e)
		require.Equal(t, 1, p.Total)
		require.Equal(t, int64(3), p.Items[0].UserID)
	}
	p, err = r.administrationAIUsers(ctx, 2, nil, domain.AdministrationUserFilter{Limit: 25, Query: "%missing"}, clock)
	require.NoError(t, err)
	require.Zero(t, p.Total)
	now = now.Add(168 * time.Hour)
	p, err = r.administrationAIUsers(ctx, 2, nil, filter, clock)
	require.NoError(t, err)
	require.Nil(t, p.PeriodID)
	require.Zero(t, p.Items[1].Weekly.Used)
	require.Equal(t, int64(30), p.Items[1].Weekly.CarriedReserved)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_weekly_periods`).Scan(&count))
	require.Equal(t, 3, count)
}

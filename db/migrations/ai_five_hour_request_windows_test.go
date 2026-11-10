package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestAIFiveHourRequestWindowMigrationPreservesFinancialHistory(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "ai_request_window_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	_, err = db.ExecContext(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY); CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	files := fstest.MapFS{}
	for _, name := range []string{"20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql"} {
		data, err := os.ReadFile(name)
		require.NoError(t, err)
		files[name] = &fstest.MapFile{Data: data}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	require.NoError(t, err)
	_, err = provider.UpTo(ctx, 20261020100300)
	require.NoError(t, err)
	org, week, old, current, op, attempt := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	anchor := time.Date(2026, 10, 20, 9, 31, 0, 0, time.UTC)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations VALUES($1)`, []any{org}},
		{`INSERT INTO users(id,organization_id) VALUES(1,$1)`, []any{org}},
		{`INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,1000,100)`, []any{org}},
		{`INSERT INTO organization_ai_weekly_anchors VALUES($1,$2)`, []any{org, anchor}},
		{`INSERT INTO organization_ai_weekly_periods(id,organization_id,anchor_at,starts_at,ends_at,reserved_microcredits) VALUES($1,$2,$3,$3,$4,80)`, []any{week, org, anchor, anchor.Add(168 * time.Hour)}},
		{`INSERT INTO user_ai_five_hour_anchors VALUES(1,$1)`, []any{anchor}},
		{`INSERT INTO user_ai_five_hour_periods(id,user_id,anchor_at,starts_at,ends_at,reserved_microcredits,carried_reserved_microcredits) VALUES($1,1,$2,$2,$3,80,0),($4,1,$2,$3,$5,80,80)`, []any{old, anchor, anchor.Add(5 * time.Hour), current, anchor.Add(10 * time.Hour)}},
		{`INSERT INTO ai_usage_operations(id,organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,$2,1,'user','chat','old',decode(repeat('ab',32),'hex'))`, []any{op, org}},
		{`INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,company_period_id,five_hour_period_id) VALUES($1,$2,$3,1,'nitec','chat','model','credits-v1','test',1000,$4,$5)`, []any{attempt, op, org, week, old}},
		{`INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,80)`, []any{attempt}},
	} {
		_, err = db.ExecContext(ctx, statement.sql, statement.args...)
		require.NoError(t, err)
	}
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	var held, carried int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, current).Scan(&held, &carried))
	require.Zero(t, held)
	require.Zero(t, carried)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reserved_microcredits FROM ai_usage_reservations WHERE attempt_id=$1 AND status='active'`, attempt).Scan(&held))
	require.EqualValues(t, 80, held)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, week).Scan(&held))
	require.EqualValues(t, 80, held)
	// An aligned history can be safely rolled back and upgraded again.
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, current).Scan(&held, &carried))
	require.EqualValues(t, 80, held)
	require.EqualValues(t, 80, carried)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO user_ai_five_hour_periods(user_id,anchor_at,starts_at,ends_at) VALUES(1,$1,$2,$3)`, anchor, anchor.Add(11*time.Hour), anchor.Add(16*time.Hour))
	require.NoError(t, err)
	requireSQLState(t, db, "23514", `UPDATE user_ai_five_hour_periods SET starts_at=starts_at+interval '1 minute',ends_at=ends_at+interval '1 minute' WHERE id=$1`, old)
	_, err = provider.Down(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "request-started history")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reserved_microcredits FROM ai_usage_reservations WHERE attempt_id=$1`, attempt).Scan(&held))
	require.EqualValues(t, 80, held)
}

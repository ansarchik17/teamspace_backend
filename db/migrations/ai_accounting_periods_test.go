package migrations_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestAIAccountingPeriodsMigration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "ai_periods_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, cleanupErr)
	})
	cfg.RuntimeParams["search_path"] = schema
	// Exact elapsed hours must not depend on the connection's DST rules.
	cfg.RuntimeParams["timezone"] = "America/New_York"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	_, err = db.ExecContext(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY);
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ,is_active BOOLEAN NOT NULL DEFAULT true);`)
	require.NoError(t, err)
	files := fstest.MapFS{}
	for _, name := range []string{"20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql"} {
		data, readErr := os.ReadFile(name)
		require.NoError(t, readErr)
		files[name] = &fstest.MapFile{Data: data}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	a, b := uuid.NewString(), uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO organizations VALUES($1),($2)`, a, b)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,100,100),($2,100,100)`, a, b)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO users(id,organization_id) VALUES(1,$1),(2,$2)`, a, b)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,1,100,100),($2,2,100,100)`, a, b)
	require.NoError(t, err)

	tables := []string{"organization_ai_weekly_anchors", "user_ai_five_hour_anchors", "organization_ai_weekly_periods", "organization_ai_user_weekly_usage", "organization_ai_service_weekly_usage", "user_ai_five_hour_periods"}
	for _, table := range tables {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, "migration must not open anchors or windows")
	}
	anchor := time.Date(2026, 10, 31, 23, 30, 0, 123456000, time.UTC)
	insertCompanyAnchor := `INSERT INTO organization_ai_weekly_anchors(organization_id,anchor_at) VALUES($1,$2)`
	requireSQLState(t, db, "23514", insertCompanyAnchor, a, "infinity")
	requireSQLState(t, db, "23514", insertCompanyAnchor, a, "-infinity")
	requireSQLState(t, db, "23503", insertCompanyAnchor, uuid.NewString(), anchor)
	_, err = db.ExecContext(ctx, insertCompanyAnchor, a, anchor)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, insertCompanyAnchor, b, anchor)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertCompanyAnchor, a, anchor.Add(time.Second))
	_, err = db.ExecContext(ctx, `INSERT INTO user_ai_five_hour_anchors(user_id,anchor_at) VALUES(1,$1),(2,$1)`, anchor)
	require.NoError(t, err)
	requireSQLState(t, db, "23503", `INSERT INTO user_ai_five_hour_anchors(user_id,anchor_at) VALUES(999,$1)`, anchor)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_weekly_anchors SET anchor_at=$2 WHERE organization_id=$1`, a, anchor.Add(time.Second))
	requireSQLState(t, db, "23514", `UPDATE user_ai_five_hour_anchors SET anchor_at=$1 WHERE user_id=1`, anchor.Add(time.Second))

	for _, window := range []struct {
		name, table, owner string
		id                 any
		duration           time.Duration
	}{
		{"week", "organization_ai_weekly_periods", "organization_id", a, 168 * time.Hour},
		{"five_hour", "user_ai_five_hour_periods", "user_id", int64(1), 5 * time.Hour},
	} {
		t.Run(window.name, func(t *testing.T) {
			insert := `INSERT INTO ` + window.table + `(` + window.owner + `,anchor_at,starts_at,ends_at) VALUES($1,$2,$3,$4)`
			end := anchor.Add(window.duration)
			for _, bounds := range [][2]time.Time{
				{anchor, anchor},
				{anchor, anchor.Add(-time.Second)},
				{anchor, end.Add(-time.Microsecond)},
				{anchor, end.Add(time.Microsecond)},
				{anchor.Add(-window.duration), anchor},
				{anchor.Add(time.Microsecond), end.Add(time.Microsecond)},
			} {
				requireSQLState(t, db, "23514", insert, window.id, anchor, bounds[0], bounds[1])
			}
			requireSQLState(t, db, "23514", insert, window.id, anchor, "infinity", "infinity")
			requireSQLState(t, db, "23503", insert, window.id, anchor.Add(time.Second), anchor.Add(time.Second), end.Add(time.Second))
			_, err := db.ExecContext(ctx, insert, window.id, anchor, anchor, end)
			require.NoError(t, err)
			requireSQLState(t, db, "23505", insert, window.id, anchor, anchor, end)
			// Idempotent concurrent creation: one row, all callers succeed.
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, createErr := db.ExecContext(ctx, insert+` ON CONFLICT (`+window.owner+`,starts_at) DO NOTHING`, window.id, anchor, end, end.Add(window.duration))
					results <- createErr
				}()
			}
			wg.Wait()
			close(results)
			for createErr := range results {
				require.NoError(t, createErr)
			}
			var count int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM `+window.table+` WHERE `+window.owner+`=$1`, window.id).Scan(&count))
			require.Equal(t, 2, count)
			// The boundary is owned by the next half-open interval only.
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM `+window.table+` WHERE `+window.owner+`=$1 AND starts_at <= $2 AND ends_at > $2`, window.id, end).Scan(&count))
			require.Equal(t, 1, count)
			// Skipped windows are allowed; first use after idle does not move anchor.
			_, err = db.ExecContext(ctx, insert, window.id, anchor, anchor.Add(10*window.duration), anchor.Add(11*window.duration))
			require.NoError(t, err)
			requireSQLState(t, db, "23514", `UPDATE `+window.table+` SET starts_at=starts_at+interval '1 microsecond',ends_at=ends_at+interval '1 microsecond' WHERE `+window.owner+`=$1`, window.id)
		})
	}
	var weekID string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM organization_ai_weekly_periods WHERE organization_id=$1 AND starts_at=$2`, a, anchor).Scan(&weekID))
	insertUsage := `INSERT INTO organization_ai_user_weekly_usage(organization_id,period_id,user_id) VALUES($1,$2,$3)`
	_, err = db.ExecContext(ctx, insertUsage, a, weekID, 1)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertUsage, a, weekID, 1)
	requireSQLState(t, db, "23503", insertUsage, b, weekID, 2)
	requireSQLState(t, db, "23503", insertUsage, a, weekID, 2)
	_, err = db.ExecContext(ctx, `INSERT INTO organization_ai_service_weekly_usage(organization_id,period_id) VALUES($1,$2)`, a, weekID)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", `INSERT INTO organization_ai_service_weekly_usage(organization_id,period_id) VALUES($1,$2)`, a, weekID)
	requireSQLState(t, db, "23503", `INSERT INTO organization_ai_service_weekly_usage(organization_id,period_id) VALUES($1,$2)`, b, weekID)

	for _, table := range []string{"organization_ai_weekly_periods", "organization_ai_user_weekly_usage", "organization_ai_service_weekly_usage", "user_ai_five_hour_periods"} {
		t.Run(table+"_counters", func(t *testing.T) {
			for _, field := range []string{"used_microcredits", "reserved_microcredits", "carried_reserved_microcredits"} {
				requireSQLState(t, db, "23514", `UPDATE `+table+` SET `+field+`=-1`)
				requireSQLState(t, db, "23502", `UPDATE `+table+` SET `+field+`=NULL`)
			}
			requireSQLState(t, db, "23514", `UPDATE `+table+` SET reserved_microcredits=1,carried_reserved_microcredits=2`)
			_, err := db.ExecContext(ctx, `UPDATE `+table+` SET used_microcredits=0,reserved_microcredits=9223372036854775807,carried_reserved_microcredits=9223372036854775807`)
			require.NoError(t, err)
			requireSQLState(t, db, "23514", `UPDATE `+table+` SET used_microcredits=1`)
			_, err = db.ExecContext(ctx, `UPDATE `+table+` SET used_microcredits=9223372036854775807,reserved_microcredits=0,carried_reserved_microcredits=0`)
			require.NoError(t, err, "confirmed over-quota consumption must not be truncated")
			_, err = db.ExecContext(ctx, `UPDATE `+table+` SET used_microcredits=200,reserved_microcredits=30,carried_reserved_microcredits=10`)
			require.NoError(t, err)
		})
	}
	assertRevision(t, db, `SELECT revision FROM organization_ai_budgets WHERE organization_id=$1`, 1, a)
	assertRevision(t, db, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, 1, a)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_user_weekly_usage SET user_id=2`)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_service_weekly_usage SET organization_id=$1`, b)
	_, err = db.ExecContext(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, b)
	require.NoError(t, err)
	var used int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT used_microcredits FROM organization_ai_user_weekly_usage WHERE organization_id=$1 AND period_id=$2 AND user_id=1`, a, weekID).Scan(&used))
	require.Equal(t, int64(200), used, "transfer must preserve old company usage")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT used_microcredits FROM user_ai_five_hour_periods WHERE user_id=1 AND starts_at=$1`, anchor).Scan(&used))
	require.Equal(t, int64(200), used, "personal five-hour history survives transfer")
	requireSQLState(t, db, "23503", `DELETE FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, a)
	requireSQLState(t, db, "23503", `DELETE FROM organization_ai_weekly_periods WHERE id=$1`, weekID)
	requireSQLState(t, db, "23503", `DELETE FROM user_ai_five_hour_anchors WHERE user_id=1`)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	var budgetCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM organization_ai_budgets`).Scan(&budgetCount))
	require.Equal(t, 2, budgetCount, "period rollback preserves settings")
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err, "reapplying all current migrations is a no-op")
}

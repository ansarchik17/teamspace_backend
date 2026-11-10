package migrations_test

import (
	"context"
	"database/sql"
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

func TestAIBudgetSettingsMigration(t *testing.T) {
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
	schema := "ai_budget_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, cleanupErr)
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	_, err = db.ExecContext(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY);
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ,is_active BOOLEAN NOT NULL DEFAULT true);`)
	require.NoError(t, err)
	a, b := uuid.NewString(), uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO organizations VALUES($1),($2)`, a, b)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO users(id,organization_id) VALUES(1,$1),(2,$2),(3,NULL)`, a, b)
	require.NoError(t, err)
	const filename = "20261020100100_ai_budget_settings.sql"
	data, err := os.ReadFile(filename)
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{filename: &fstest.MapFile{Data: data}})
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM organization_ai_budgets`).Scan(&count))
	require.Zero(t, count, "migration must not grant existing companies any credit")

	insertBudget := `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,$2,$3)`
	requireSQLState(t, db, "23514", insertBudget, a, -1, 0)
	requireSQLState(t, db, "23514", insertBudget, a, int64(1000000000000001), 0)
	requireSQLState(t, db, "23502", insertBudget, a, nil, 0)
	requireSQLState(t, db, "23503", insertBudget, uuid.NewString(), 0, 0)
	_, err = db.ExecContext(ctx, insertBudget, a, int64(1000000000000000), int64(1000000000000000))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, insertBudget, b, 0, 0)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertBudget, a, 0, 0)
	for _, column := range []string{"weekly_budget_microcredits", "max_five_hour_microcredits", "service_weekly_quota_microcredits"} {
		for _, value := range []int64{-1, 1000000000000001} {
			requireSQLState(t, db, "23514", `UPDATE organization_ai_budgets SET `+column+`=$2 WHERE organization_id=$1`, a, value)
		}
	}
	var serviceQuota int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT service_weekly_quota_microcredits FROM organization_ai_budgets WHERE organization_id=$1`, a).Scan(&serviceQuota))
	require.Zero(t, serviceQuota)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_budgets SET service_weekly_quota_microcredits=-1 WHERE organization_id=$1`, a)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_budgets SET max_five_hour_microcredits=-1 WHERE organization_id=$1`, a)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_budgets SET revision=0 WHERE organization_id=$1`, a)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_budgets SET organization_id=$1 WHERE organization_id=$2`, uuid.NewString(), a)
	_, err = db.ExecContext(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=0 WHERE organization_id=$1`, a)
	require.NoError(t, err)
	assertRevision(t, db, `SELECT revision FROM organization_ai_budgets WHERE organization_id=$1`, 2, a)
	_, err = db.ExecContext(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=0 WHERE organization_id=$1`, a)
	require.NoError(t, err)
	assertRevision(t, db, `SELECT revision FROM organization_ai_budgets WHERE organization_id=$1`, 2, a)

	insertQuota := `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,$2,$3,$4)`
	requireSQLState(t, db, "23514", insertQuota, a, 2, 0, 0)
	requireSQLState(t, db, "23514", insertQuota, a, 3, 0, 0)
	requireSQLState(t, db, "23514", insertQuota, a, 1, nil, 0)
	requireSQLState(t, db, "23514", insertQuota, a, 1, -1, 0)
	requireSQLState(t, db, "23514", insertQuota, a, 1, 0, int64(1000000000000001))
	_, err = db.ExecContext(ctx, insertQuota, a, 1, 100, 20)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertQuota, a, 1, 0, 0)
	for _, column := range []string{"weekly_quota_microcredits", "five_hour_limit_microcredits"} {
		for _, value := range []int64{-1, 1000000000000001} {
			requireSQLState(t, db, "23514", `UPDATE organization_ai_user_quotas SET `+column+`=$2 WHERE organization_id=$1 AND user_id=1`, a, value)
		}
	}
	requireSQLState(t, db, "23514", `UPDATE organization_ai_user_quotas SET revision=0 WHERE organization_id=$1 AND user_id=1`, a)
	_, err = db.ExecContext(ctx, `INSERT INTO users(id,organization_id) VALUES(4,$1)`, a)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE users SET organization_id=$1 WHERE id=4`, b)
	require.NoError(t, err)
	assignment := make(chan error, 1)
	go func() {
		_, assignErr := db.ExecContext(ctx, insertQuota, a, 4, 0, 0)
		assignment <- assignErr
	}()
	require.NoError(t, tx.Commit())
	var membershipErr interface{ SQLState() string }
	require.ErrorAs(t, <-assignment, &membershipErr)
	require.Equal(t, "23514", membershipErr.SQLState(), "concurrent assignment cannot survive transfer")
	_, err = db.ExecContext(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=0 WHERE organization_id=$1 AND user_id=1`, a)
	require.NoError(t, err, "lowering Q retains H; effective limit is enforced by the service")
	assertRevision(t, db, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, 2, a)
	_, err = db.ExecContext(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, b)
	require.NoError(t, err)
	var q, h sql.NullInt64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT weekly_quota_microcredits,five_hour_limit_microcredits FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, a).Scan(&q, &h))
	require.False(t, q.Valid)
	require.False(t, h.Valid)
	assertRevision(t, db, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, 3, a)
	requireSQLState(t, db, "23514", `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=1,five_hour_limit_microcredits=1 WHERE organization_id=$1 AND user_id=1`, a)
	_, err = db.ExecContext(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, a)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=1,five_hour_limit_microcredits=1 WHERE organization_id=$1 AND user_id=1`, a)
	require.NoError(t, err)
	assertRevision(t, db, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, 4, a)
	_, err = db.ExecContext(ctx, `UPDATE users SET is_active=false WHERE id=1`)
	require.NoError(t, err)
	assertRevision(t, db, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, 4, a)
	_, err = db.ExecContext(ctx, `UPDATE users SET deleted_at=now() WHERE id=1`)
	require.NoError(t, err)
	assertRevision(t, db, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=1`, 5, a)
	requireSQLState(t, db, "23514", insertQuota, a, 1, 0, 0)
	requireSQLState(t, db, "23503", `DELETE FROM users WHERE id=1`)
	requireSQLState(t, db, "23503", `DELETE FROM organization_ai_budgets WHERE organization_id=$1`, a)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE users SET organization_id=NULL,deleted_at=NULL WHERE id=1`)
	require.NoError(t, err, "rollback removes membership trigger")
	_, err = provider.Up(ctx)
	require.NoError(t, err, "migration can be reapplied after rollback")
}

func requireSQLState(t *testing.T, db *sql.DB, code, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	require.Error(t, err)
	var pgErr interface{ SQLState() string }
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, code, pgErr.SQLState())
}

func assertRevision(t *testing.T, db *sql.DB, query string, expected int64, args ...any) {
	t.Helper()
	var revision int64
	require.NoError(t, db.QueryRow(query, args...).Scan(&revision))
	require.Equal(t, expected, revision)
}

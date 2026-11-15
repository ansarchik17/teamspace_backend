package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestStartupMigrationSwitchIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_API_MIGRATIONS_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_API_MIGRATIONS_DATABASE_URL to a fresh disposable unitespace_migrations_test database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path != "/unitespace_migrations_test" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatal("requires the dedicated local unitespace_migrations_test database")
	}
	password, _ := u.User.Password()
	cfg := config.DatabaseConfig{Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: password, Database: "unitespace_migrations_test", SSLMode: "disable"}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var empty bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.goose_db_version') IS NULL AND to_regclass('public.startup_migration_probe') IS NULL`).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	if !empty {
		t.Fatal("integration fixture requires a fresh migration ledger")
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS startup_migration_probe, goose_db_version`)
	}()
	dir := t.TempDir()
	writeMigration := func(name, sql string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeMigration("00001_probe.sql", "-- +goose Up\nCREATE TABLE startup_migration_probe(value INTEGER);\nINSERT INTO startup_migration_probe VALUES(1);\n-- +goose Down\nDROP TABLE startup_migration_probe;\n")
	t.Setenv("MIGRATIONS_DIR", dir)
	t.Setenv("RUN_DB_MIGRATIONS", "false")
	if err := runStartupMigrations(ctx, cfg, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.goose_db_version') IS NULL AND to_regclass('public.startup_migration_probe') IS NULL`).Scan(&empty); err != nil || !empty {
		t.Fatalf("disabled startup created schema or ledger: empty=%v err=%v", empty, err)
	}

	// The unset/default behavior still prepares a fresh local development DB.
	t.Setenv("RUN_DB_MIGRATIONS", "")
	if err := runStartupMigrations(ctx, cfg, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	writeMigration("00002_pending.sql", "-- +goose Up\nINSERT INTO startup_migration_probe VALUES(2);\n-- +goose Down\nDELETE FROM startup_migration_probe WHERE value=2;\n")
	var count int
	assertRows := func(want int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM startup_migration_probe`).Scan(&count); err != nil || count != want {
			t.Fatalf("probe rows=%d want=%d err=%v", count, want, err)
		}
	}
	assertRows(1)

	// A later API start must not apply a pending migration when disabled.
	t.Setenv("RUN_DB_MIGRATIONS", "false")
	if err := runStartupMigrations(ctx, cfg, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	assertRows(1)
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id=2 AND is_applied`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled startup changed the migration ledger: count=%d err=%v", count, err)
	}

	t.Setenv("RUN_DB_MIGRATIONS", "true")
	for i := 0; i < 2; i++ {
		if err := runStartupMigrations(ctx, cfg, zap.NewNop()); err != nil {
			t.Fatal(err)
		}
		assertRows(2)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id=2 AND is_applied`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration must apply exactly once: count=%d err=%v", count, err)
	}
	writeMigration("00003_broken.sql", "-- +goose Up\nINSERT INTO nonexistent_migration_probe VALUES(3);\n")
	if err := runStartupMigrations(ctx, cfg, zap.NewNop()); err == nil {
		t.Fatal("enabled startup ignored a failed migration")
	}
	assertRows(2)
}

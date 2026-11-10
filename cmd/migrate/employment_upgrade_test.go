package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestEmploymentAdmissionUpgradeFromReleasedCompanyServicesIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := "employment_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../db/migrations"),
		goose.WithExcludeVersions([]int64{20261022120000}))
	if err != nil {
		t.Fatal(err)
	}
	// Keep the released baseline fixed while checking every newer migration too.
	var targetVersion int64
	for _, source := range provider.ListSources() {
		if source.Version > targetVersion {
			targetVersion = source.Version
		}
	}
	if _, err := provider.UpTo(ctx, 20261027000300); err != nil {
		t.Fatalf("released company-services baseline: %v", err)
	}
	var baseline int64
	if err := db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&baseline); err != nil || baseline != 20261027000300 {
		t.Fatalf("released baseline=%d, err=%v", baseline, err)
	}
	var missing int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=20261022120000 AND is_applied").Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("baseline must exclude employment migration: applied=%d, err=%v", missing, err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO organizations(id,name,slug) VALUES('10000000-0000-0000-0000-000000000001','Employment upgrade','employment-upgrade');
INSERT INTO users(id,email,username,password_hash,first_name,last_name,organization_id)
VALUES(900001,'employment@example.invalid','employment-fixture','test-only','Migration','Fixture','10000000-0000-0000-0000-000000000001');`); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(u)::text FROM users u WHERE id=900001").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := applyMigrations(ctx, db, "../../db/migrations"); err != nil {
			t.Fatalf("employment upgrade run %d: %v", i+1, err)
		}
	}
	var applied int
	var version int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=20261022120000 AND is_applied").Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("employment migration applied %d times: %v", applied, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil || version != targetVersion {
		t.Fatalf("fully migrated version=%d, want=%d, err=%v", version, targetVersion, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(u)::text FROM users u WHERE id=900001").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("upgrade changed the existing employee")
	}
	var allowed bool
	if err := db.QueryRowContext(ctx, "SELECT workspace_user_can_login_at(900001, '2026-10-27T00:00:00Z')").Scan(&allowed); err != nil || !allowed {
		t.Fatalf("existing employee login: allowed=%v, err=%v", allowed, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date)
VALUES('10000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',900001,900001,'2026-10-28')`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT workspace_user_can_login_at(900001, '2026-10-27T00:00:00Z')").Scan(&allowed); err != nil || allowed {
		t.Fatalf("future admission must block login: allowed=%v, err=%v", allowed, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT workspace_user_can_login_at(900001, '2026-10-28T00:00:00Z')").Scan(&allowed); err != nil || !allowed {
		t.Fatalf("admission start must allow login: allowed=%v, err=%v", allowed, err)
	}
	// A neighbouring, unreviewed gap must remain blocked.
	if _, err := db.ExecContext(ctx, "DELETE FROM goose_db_version WHERE version_id=20261022100500"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, db, "../../db/migrations"); err == nil || !strings.Contains(err.Error(), "unexpected missing migration 20261022100500") {
		t.Fatalf("unreviewed gap must fail closed: %v", err)
	}
}

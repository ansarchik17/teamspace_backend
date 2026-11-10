package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// This opt-in test uses only the named disposable database created by the
// release rehearsal. It never accepts the application's production DSN.
func TestAIUpgradeFromReleasedIForm(t *testing.T) {
	dsn := os.Getenv("TEST_AI_MIGRATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_AI_MIGRATION_DATABASE_URL to a disposable database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var database string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "ai_migration_upgrade_test" {
		t.Fatal("requires dedicated ai_migration_upgrade_test database")
	}
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("requires an empty disposable database")
	}

	baseline := t.TempDir()
	files, err := filepath.Glob("../../db/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) >= "20261020100100" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(baseline, filepath.Base(file)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyMigrations(ctx, db, baseline); err != nil {
		t.Fatalf("released baseline: %v", err)
	}
	var version int64
	if err := db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 20261019000100 {
		t.Fatalf("unexpected released version %d", version)
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO organizations(id,name,slug) VALUES('10000000-0000-0000-0000-000000000001','Migration fixture','migration-fixture');
INSERT INTO users(id,email,username,password_hash,first_name,last_name,organization_id)
VALUES(900001,'migration@example.invalid','migration-fixture','test-only','Migration','Fixture','10000000-0000-0000-0000-000000000001');
INSERT INTO iform_operation_receipts(organization_id,request_id,step_id,occurrence,actor_id,operation,request_hash,response)
VALUES('10000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','fixture',1,900001,'fixture','unchanged','{"preserve":true}');`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRowContext(ctx, "SELECT row_to_json(t)::text FROM iform_operation_receipts t").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, db, "../../db/migrations"); err != nil {
		t.Fatalf("AI upgrade: %v", err)
	}
	for _, version := range domain.AIAccountingMigrationVersions {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=$1 AND is_applied", version).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("AI migration %d applied %d times", version, count)
		}
	}
	var after string
	if err := db.QueryRowContext(ctx, "SELECT row_to_json(t)::text FROM iform_operation_receipts t").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("released iForm receipt changed")
	}
	var ledgerBefore, ledgerAfter int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version").Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, db, "../../db/migrations"); err != nil {
		t.Fatalf("repeated upgrade: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version").Scan(&ledgerAfter); err != nil {
		t.Fatal(err)
	}
	if ledgerBefore != ledgerAfter {
		t.Fatal("repeated upgrade changed migration ledger")
	}

	// Do not fix the incident by silently allowing arbitrary missing migrations.
	gap := filepath.Join(baseline, "20261018000100_unexpected_gap.sql")
	if err := os.WriteFile(gap, []byte("-- +goose Up\nCREATE TABLE unsafe_gap(id int);\n-- +goose Down\nDROP TABLE unsafe_gap;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err = applyMigrations(ctx, db, baseline)
	if err == nil || !strings.Contains(err.Error(), "unexpected missing migration 20261018000100") {
		t.Fatalf("unexpected gap must fail closed: %v", err)
	}
}

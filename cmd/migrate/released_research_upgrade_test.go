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

// Reproduce the released production ledger: research jobs were deployed at
// 20261023000100 before the earlier audio-measurement migration was included.
func TestAudioMeasurementUpgradeFromReleasedResearchIntegration(t *testing.T) {
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
	schema := "research_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE goose_db_version(id BIGSERIAL PRIMARY KEY,version_id BIGINT NOT NULL,is_applied BOOLEAN NOT NULL,tstamp TIMESTAMP NOT NULL DEFAULT now()); INSERT INTO goose_db_version(version_id,is_applied) VALUES(0,true)`); err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../db/migrations"), goose.WithExcludeVersions([]int64{20261022100600}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20261023000100); err != nil {
		t.Fatalf("released research baseline: %v", err)
	}
	var baseline int64
	if err := db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&baseline); err != nil || baseline != 20261023000100 {
		t.Fatalf("baseline=%d err=%v", baseline, err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO organizations(id,name,slug) VALUES('10000000-0000-0000-0000-000000000001','Upgrade fixture','research-upgrade-fixture');
INSERT INTO organization_ai_weekly_anchors(organization_id,anchor_at) VALUES('10000000-0000-0000-0000-000000000001','2026-10-01T00:00:00Z');
INSERT INTO organization_ai_weekly_periods(id,organization_id,anchor_at,starts_at,ends_at)
VALUES('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z','2026-10-08T00:00:00Z');
INSERT INTO ai_usage_operations(id,organization_id,payer_kind,operation_kind,idempotency_key,request_fingerprint)
VALUES('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','service','fixture','released-fixture',decode(repeat('ab',32),'hex'));
INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,output_weight_microcredits,company_period_id,input_tokens,output_tokens,total_tokens,usage_status,usage_source,charged_microcredits,status,completed_at)
VALUES('40000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001',1,'fixture','chat','fixture-model','credits-v1','fixture',1000,1000,'20000000-0000-0000-0000-000000000001',10,20,30,'verified','provider',30,'settled',now());`); err != nil {
		t.Fatalf("seed released accounting history: %v", err)
	}
	var before, after string
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(a)::text FROM ai_usage_attempts a").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := applyMigrations(ctx, db, "../../db/migrations"); err != nil {
			t.Fatalf("research upgrade run %d: %v", i+1, err)
		}
	}
	if err := db.QueryRowContext(ctx, "SELECT (to_jsonb(a)-'measured_unit'-'measured_quantity')::text FROM ai_usage_attempts a").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("upgrade changed settled accounting history")
	}
	var applied, recoveryTables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=20261022100600 AND is_applied").Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("audio migration applied %d times: %v", applied, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name IN ('organization_invitations','user_recovery_emails','user_credential_state')", schema).Scan(&recoveryTables); err != nil || recoveryTables != 3 {
		t.Fatalf("invitation/recovery tables=%d: %v", recoveryTables, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE ai_usage_attempts SET charged_microcredits=31"); err == nil {
		t.Fatal("settled history must remain immutable after the upgrade")
	}
	// A neighbouring unreviewed AI migration must still fail closed.
	if _, err := db.ExecContext(ctx, "DELETE FROM goose_db_version WHERE version_id=20261022100500"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, db, "../../db/migrations"); err == nil || !strings.Contains(err.Error(), "unexpected missing migration 20261022100500") {
		t.Fatalf("unreviewed gap must be rejected: %v", err)
	}
}

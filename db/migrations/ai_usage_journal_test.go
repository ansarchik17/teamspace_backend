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

func TestAIUsageJournalMigration(t *testing.T) {
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
	schema := "ai_journal_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	files := fstest.MapFS{}
	for _, name := range []string{"026_create_ai_tables.sql", "029_create_unified_ai_chats.sql", "20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql"} {
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
	_, err = db.ExecContext(ctx, `INSERT INTO users(id,organization_id) VALUES(1,$1),(2,$2)`, a, b)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,1000000,1000000),($2,1000000,1000000)`, a, b)
	require.NoError(t, err)
	anchor := time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)
	_, err = db.ExecContext(ctx, `INSERT INTO organization_ai_weekly_anchors VALUES($1,$3),($2,$3)`, a, b, anchor)
	require.NoError(t, err)
	weekA, weekB, fiveA, fiveB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO organization_ai_weekly_periods(id,organization_id,anchor_at,starts_at,ends_at) VALUES($1,$2,$3,$3,$4),($5,$6,$3,$3,$4)`, weekA, a, anchor, anchor.Add(168*time.Hour), weekB, b)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO user_ai_five_hour_anchors VALUES(1,$1),(2,$1)`, anchor)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO user_ai_five_hour_periods(id,user_id,anchor_at,starts_at,ends_at) VALUES($1,1,$3,$3,$4),($2,2,$3,$3,$4)`, fiveA, fiveB, anchor, anchor.Add(5*time.Hour))
	require.NoError(t, err)

	insertOp := `INSERT INTO ai_usage_operations(id,organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint,source_kind,source_id) VALUES($1,$2,$3,'user','chat',$4,decode(repeat('ab',32),'hex'),'ai_chat',$5)`
	op, chat := uuid.NewString(), uuid.NewString()
	_, err = db.ExecContext(ctx, insertOp, op, a, 1, "request-1", chat)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertOp, op, a, 1, "other-key", chat)
	requireSQLState(t, db, "23505", insertOp, uuid.NewString(), a, 1, "request-1", chat)
	requireSQLState(t, db, "23505", insertOp, uuid.NewString(), b, 1, "request-1", chat)
	_, err = db.ExecContext(ctx, insertOp, uuid.NewString(), b, 2, "request-1", chat)
	require.NoError(t, err, "idempotency keys are scoped by payer")
	requireSQLState(t, db, "23514", `UPDATE ai_usage_operations SET organization_id=$1 WHERE id=$2`, b, op)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_operations SET request_fingerprint=decode(repeat('cd',32),'hex') WHERE id=$1`, op)
	requireSQLState(t, db, "23514", insertOp, uuid.NewString(), a, nil, "missing-user", chat)
	requireSQLState(t, db, "23514", `INSERT INTO ai_usage_operations(organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,1,'user','chat','bad-fingerprint',decode('ab','hex'))`, a)

	// Concurrent retries of one operation create exactly one row.
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, createErr := db.ExecContext(ctx, insertOp+` ON CONFLICT DO NOTHING`, uuid.NewString(), a, 1, "concurrent", chat)
			results <- createErr
		}()
	}
	wg.Wait()
	close(results)
	for createErr := range results {
		require.NoError(t, createErr)
	}
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ai_usage_operations WHERE user_id=1 AND idempotency_key='concurrent'`).Scan(&count))
	require.Equal(t, 1, count)

	insertAttempt := `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,output_weight_microcredits,company_period_id,five_hour_period_id)
VALUES($1,$2,$3,$4,'nitec','chat','openai/gpt-oss-120b','credits-v1','nitec-chat-v1',1000,3000,$5,$6)`
	attempt := uuid.NewString()
	_, err = db.ExecContext(ctx, insertAttempt, attempt, op, a, 1, weekA, fiveA)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertAttempt, uuid.NewString(), op, a, 1, weekA, fiveA)
	requireSQLState(t, db, "23503", insertAttempt, uuid.NewString(), op, b, 2, weekB, fiveA)
	requireSQLState(t, db, "23503", insertAttempt, uuid.NewString(), op, a, 2, weekB, fiveA)
	requireSQLState(t, db, "23514", insertAttempt, uuid.NewString(), op, a, 2, weekA, fiveB)
	requireSQLState(t, db, "23514", insertAttempt, uuid.NewString(), op, a, 2, weekA, nil)
	reserve := uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO ai_usage_reservations(id,attempt_id,reserved_microcredits) VALUES($1,$2,50000)`, reserve, attempt)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,50000)`, attempt)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_reservations SET reserved_microcredits=-1 WHERE id=$1`, reserve)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_reservations SET status='released' WHERE id=$1`, reserve)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET input_tokens=-1 WHERE id=$1`, attempt)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET cached_input_tokens=1 WHERE id=$1`, attempt)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET status='settled',charged_microcredits=0,completed_at=now() WHERE id=$1`, attempt)
	_, err = db.ExecContext(ctx, `UPDATE ai_usage_attempts SET status='unknown' WHERE id=$1`, attempt)
	require.NoError(t, err)
	var unknown bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT input_tokens IS NULL AND charged_microcredits IS NULL FROM ai_usage_attempts WHERE id=$1`, attempt).Scan(&unknown))
	require.True(t, unknown, "missing usage is not zero usage")
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET credit_policy_version='credits-v2' WHERE id=$1`, attempt)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET usage_status='verified',usage_source='response',input_tokens=11,output_tokens=7,total_tokens=19 WHERE id=$1`, attempt)
	_, err = db.ExecContext(ctx, `UPDATE ai_usage_attempts SET actual_model='openai/gpt-oss-120b',provider_request_id='response-1',status='settled',usage_status='verified',usage_source='response',reported_input_tokens=11,reported_output_tokens=7,reported_total_tokens=18,input_tokens=11,output_tokens=7,total_tokens=18,charged_microcredits=32000,completed_at=now() WHERE id=$1`, attempt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE ai_usage_reservations SET status='settled',closed_at=now() WHERE id=$1`, reserve)
	require.NoError(t, err)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET charged_microcredits=0 WHERE id=$1`, attempt)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_reservations SET status='active',closed_at=NULL WHERE id=$1`, reserve)
	second := uuid.NewString()
	_, err = db.ExecContext(ctx, insertAttempt, second, op, a, 2, weekA, fiveA)
	require.NoError(t, err, "a real retry is a separate attempt")
	requireSQLState(t, db, "23505", `UPDATE ai_usage_attempts SET provider_request_id='response-1' WHERE id=$1`, second)
	serviceOp, serviceAttempt := uuid.NewString(), uuid.NewString()
	insertServiceOp := `INSERT INTO ai_usage_operations(id,organization_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,$2,'service','drive_index','index-file-v1',decode(repeat('ab',32),'hex'))`
	_, err = db.ExecContext(ctx, insertServiceOp, serviceOp, a)
	require.NoError(t, err)
	requireSQLState(t, db, "23505", insertServiceOp, uuid.NewString(), a)
	_, err = db.ExecContext(ctx, insertServiceOp, uuid.NewString(), b)
	require.NoError(t, err, "service keys are company scoped")
	requireSQLState(t, db, "23514", insertAttempt, uuid.NewString(), serviceOp, a, 1, weekA, fiveA)
	_, err = db.ExecContext(ctx, insertAttempt, serviceAttempt, serviceOp, a, 1, weekA, nil)
	require.NoError(t, err, "service payer does not need a personal period")
	requireSQLState(t, db, "23514", `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,0)`, serviceAttempt)
	_, err = db.ExecContext(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,9223372036854775807)`, serviceAttempt)
	require.NoError(t, err)
	requireSQLState(t, db, "23514", `UPDATE ai_usage_attempts SET status='rejected',usage_status='confirmed_not_executed',charged_microcredits=1,completed_at=now() WHERE id=$1`, serviceAttempt)
	_, err = db.ExecContext(ctx, `UPDATE ai_usage_attempts SET status='waived',usage_status='platform_absorbed',charged_microcredits=0,completed_at=now() WHERE id=$1`, serviceAttempt)
	require.NoError(t, err, "platform absorption does not fabricate zero token usage")

	// Use actual chat migrations and their cascading message deletion.
	_, err = db.ExecContext(ctx, `INSERT INTO ai_conversations(id,user_id,model_name) VALUES($1,1,'model')`, chat)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO ai_messages(conversation_id,role,content,tokens_usage) VALUES($1,'assistant','reply',18)`, chat)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO unified_ai_chats(id,user_id) VALUES($1,1)`, chat)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO unified_ai_messages(chat_id,role,content,tokens_usage) VALUES($1,'assistant','reply',18)`, chat)
	require.NoError(t, err)
	for _, table := range []string{"ai_conversations", "unified_ai_chats"} {
		_, err = db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=$1`, chat)
		require.NoError(t, err)
	}
	var charge int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT sum(charged_microcredits) FROM ai_usage_attempts WHERE operation_id=$1`, op).Scan(&charge))
	require.Equal(t, int64(32000), charge)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ai_usage_reservations WHERE id=$1`, reserve).Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.ExecContext(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, b)
	require.NoError(t, err)
	var historicalOrg string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT organization_id FROM ai_usage_operations WHERE id=$1`, op).Scan(&historicalOrg))
	require.Equal(t, a, historicalOrg)
	requireSQLState(t, db, "23503", `DELETE FROM ai_usage_operations WHERE id=$1`, op)
	requireSQLState(t, db, "23503", `DELETE FROM ai_usage_attempts WHERE id=$1`, attempt)
	requireSQLState(t, db, "23503", `DELETE FROM users WHERE id=1`)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM organization_ai_weekly_periods`).Scan(&count))
	require.Equal(t, 2, count, "journal rollback preserves periods")
	_, err = provider.Up(ctx)
	require.NoError(t, err)
}

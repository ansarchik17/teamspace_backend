package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDocumentJobLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DOCUMENT_SEARCH_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DOCUMENT_SEARCH_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "document_jobs_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE users(id bigint PRIMARY KEY);CREATE TABLE ai_conversations(id uuid PRIMARY KEY,user_id bigint);CREATE TABLE ai_messages(id uuid PRIMARY KEY);INSERT INTO users VALUES(42);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260925000100_ai_documents_production.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	chat := uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO ai_conversations VALUES($1,42)`, chat)
	require.NoError(t, err)
	repo := NewAIDocumentRepository(db, zap.NewNop())
	template := AIDocument{ConversationID: chat, OwnerID: 42, Name: "file.txt", DetectedType: "text/plain", Bytes: 10, SHA256: strings.Repeat("a", 64), ObjectKey: "test/object", Status: "uploaded", ParserVersion: "v1", IndexVersion: "retrieval-v2"}
	// A queue insert failure must roll back the file metadata insert as well.
	_, err = db.Exec(ctx, `ALTER TABLE ai_document_jobs ADD CONSTRAINT reject_queue CHECK(state<>'queued')`)
	require.NoError(t, err)
	_, err = repo.CreateDocument(ctx, template)
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_documents`).Scan(&count))
	require.Zero(t, count)
	_, err = db.Exec(ctx, `ALTER TABLE ai_document_jobs DROP CONSTRAINT reject_queue`)
	require.NoError(t, err)
	id, err := repo.CreateDocument(ctx, template)
	require.NoError(t, err)
	first, jobID, err := repo.ClaimNextJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 1, first.JobAttempt)
	_, err = db.Exec(ctx, `UPDATE ai_document_jobs SET lease_until=now()-INTERVAL '1 second' WHERE id=$1`, jobID)
	require.NoError(t, err)
	second, secondID, err := repo.ClaimNextJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, jobID, secondID)
	require.Equal(t, 2, second.JobAttempt)
	chunks := []AIDocumentChunk{{Ordinal: 0, Text: "Новая достоверная версия", Locator: map[string]any{"chunk": 1}, TokenCount: 3}}
	require.ErrorIs(t, repo.MarkReady(ctx, jobID, *first, chunks), ErrAIDocumentLeaseLost)
	require.ErrorIs(t, repo.MarkFailed(ctx, jobID, id, "stale", "stale", true, first.JobAttempt), ErrAIDocumentLeaseLost)
	var state string
	require.NoError(t, db.QueryRow(ctx, `SELECT state FROM ai_document_jobs WHERE id=$1`, jobID).Scan(&state))
	require.Equal(t, "processing", state)
	second.TextChars = 24
	require.NoError(t, repo.MarkReady(ctx, jobID, *second, chunks))
	require.ErrorIs(t, repo.MarkFailed(ctx, jobID, id, "late_failure", "late", true, first.JobAttempt), ErrAIDocumentLeaseLost)
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_documents WHERE id=$1`, id).Scan(&state))
	require.Equal(t, "ready", state)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_document_chunks WHERE document_id=$1`, id).Scan(&count))
	require.Equal(t, 1, count)

	// An in-flight delete must never be undone by completion or retry handling.
	template.SHA256 = strings.Repeat("b", 64)
	deletedID, err := repo.CreateDocument(ctx, template)
	require.NoError(t, err)
	deleted, deletedJob, err := repo.ClaimNextJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, deleted)
	require.NoError(t, repo.DeleteDocument(ctx, 42, chat, deletedID))
	require.Error(t, repo.MarkReady(ctx, deletedJob, *deleted, chunks))
	require.NoError(t, repo.MarkFailed(ctx, deletedJob, deletedID, "deleted", "deleted", true, deleted.JobAttempt))
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_documents WHERE id=$1`, deletedID).Scan(&state))
	require.Equal(t, "deleted", state)
	require.NoError(t, db.QueryRow(ctx, `SELECT state FROM ai_document_jobs WHERE id=$1`, deletedJob).Scan(&state))
	require.Equal(t, "failed", state)

	// Retry exhaustion is terminal, not an unclaimable queued job.
	template.SHA256 = strings.Repeat("c", 64)
	failedID, err := repo.CreateDocument(ctx, template)
	require.NoError(t, err)
	for attempt := 1; attempt <= 3; attempt++ {
		doc, job, err := repo.ClaimNextJob(ctx, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, doc)
		require.Equal(t, attempt, doc.JobAttempt)
		require.NoError(t, repo.MarkFailed(ctx, job, failedID, "storage_error", "test", true, doc.JobAttempt))
		if attempt < 3 {
			_, err = db.Exec(ctx, `UPDATE ai_document_jobs SET next_attempt_at=now()-INTERVAL '1 second' WHERE id=$1`, job)
			require.NoError(t, err)
		}
	}
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_documents WHERE id=$1`, failedID).Scan(&state))
	require.Equal(t, "failed", state)
	noJob, _, err := repo.ClaimNextJob(ctx, time.Minute)
	require.NoError(t, err)
	require.Nil(t, noJob)

	// Expired final leases must be persisted even when there is no next job.
	template.SHA256 = strings.Repeat("d", 64)
	exhaustedID, err := repo.CreateDocument(ctx, template)
	require.NoError(t, err)
	doc, exhaustedJob, err := repo.ClaimNextJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, doc)
	_, err = db.Exec(ctx, `UPDATE ai_document_jobs SET attempts=3,lease_until=now()-INTERVAL '1 second' WHERE id=$1`, exhaustedJob)
	require.NoError(t, err)
	noJob, _, err = repo.ClaimNextJob(ctx, time.Minute)
	require.NoError(t, err)
	require.Nil(t, noJob)
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_documents WHERE id=$1`, exhaustedID).Scan(&state))
	require.Equal(t, "failed", state)
	require.NoError(t, db.QueryRow(ctx, `SELECT state FROM ai_document_jobs WHERE id=$1`, exhaustedJob).Scan(&state))
	require.Equal(t, "failed", state)
}

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNotebookResearchJobRepository(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "notebook_jobs_repo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, cleanupErr)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY);
CREATE TABLE notebook_chats(id UUID PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE);
INSERT INTO users VALUES (1), (2);`)
	require.NoError(t, err)
	data, err := os.ReadFile("../../db/migrations/20261023000100_notebook_research_jobs.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	chatA, chatB := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO notebook_chats VALUES ($1, 1), ($2, 2)`, chatA, chatB)
	require.NoError(t, err)

	repo := NewNotebookResearchJobRepository(db, zap.NewNop())
	lease := time.Now().Add(time.Minute)
	newJob := func(user int64, chat uuid.UUID, op string) NotebookResearchJob {
		return NotebookResearchJob{ID: uuid.New(), UserID: user, ChatID: chat, OperationID: op, Request: json.RawMessage(`{"question":"q"}`), LeaseUntil: lease}
	}

	first, inserted, err := repo.CreateJob(ctx, newJob(1, chatA, "op-1"))
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, NotebookJobQueued, first.State)
	require.JSONEq(t, `{"question":"q"}`, string(first.Request))

	// Same operation: the existing job. Another operation while unfinished: the active job.
	again, inserted, err := repo.CreateJob(ctx, newJob(1, chatA, "op-1"))
	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, first.ID, again.ID)
	other, inserted, err := repo.CreateJob(ctx, newJob(1, chatA, "op-2"))
	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, first.ID, other.ID)
	// Another user is independent, even with the same operation id.
	foreign, inserted, err := repo.CreateJob(ctx, newJob(2, chatB, "op-1"))
	require.NoError(t, err)
	require.True(t, inserted)

	// Reads are scoped by user and notebook.
	for _, scope := range []struct {
		user int64
		chat uuid.UUID
	}{{2, chatA}, {1, chatB}} {
		got, err := repo.GetJob(ctx, scope.user, scope.chat, first.ID)
		require.NoError(t, err)
		require.Nil(t, got)
		got, cancelled, err := repo.CancelJob(ctx, scope.user, scope.chat, first.ID)
		require.NoError(t, err)
		require.False(t, cancelled)
		require.Nil(t, got)
	}

	started, err := repo.StartJob(ctx, first.ID, lease)
	require.NoError(t, err)
	require.True(t, started)
	started, err = repo.StartJob(ctx, first.ID, lease)
	require.NoError(t, err)
	require.False(t, started)
	active, err := repo.UpdateProgress(ctx, first.ID, "analyzing", 3, 8, lease)
	require.NoError(t, err)
	require.True(t, active)
	finished, err := repo.FinishJob(ctx, first.ID, NotebookJobDone, "done", json.RawMessage(`{"content":"ok"}`), nil)
	require.NoError(t, err)
	require.True(t, finished)
	got, err := repo.GetJob(ctx, 1, chatA, first.ID)
	require.NoError(t, err)
	require.Equal(t, NotebookJobDone, got.State)
	require.Equal(t, 3, got.ChunksDone)
	require.Equal(t, 8, got.ChunksTotal)
	require.JSONEq(t, `{"content":"ok"}`, string(got.Result))
	require.Nil(t, got.Error)
	require.NotNil(t, got.StartedAt)
	require.NotNil(t, got.FinishedAt)

	// A finished job is neither updated nor cancelled.
	active, err = repo.UpdateProgress(ctx, first.ID, "answering", 8, 8, lease)
	require.NoError(t, err)
	require.False(t, active)
	got, cancelled, err := repo.CancelJob(ctx, 1, chatA, first.ID)
	require.NoError(t, err)
	require.False(t, cancelled)
	require.Equal(t, NotebookJobDone, got.State)

	// The slot is free again; cancel keeps the outcome from being overwritten.
	second, inserted, err := repo.CreateJob(ctx, newJob(1, chatA, "op-2"))
	require.NoError(t, err)
	require.True(t, inserted)
	got, cancelled, err = repo.CancelJob(ctx, 1, chatA, second.ID)
	require.NoError(t, err)
	require.True(t, cancelled)
	require.Equal(t, NotebookJobCancelled, got.State)
	finished, err = repo.FinishJob(ctx, second.ID, NotebookJobFailed, "reading", nil, json.RawMessage(`{"code":"X"}`))
	require.NoError(t, err)
	require.False(t, finished)
	active, err = repo.ExtendLease(ctx, second.ID, lease)
	require.NoError(t, err)
	require.False(t, active)

	// Expired leases are failed; finished jobs are removed after retention.
	_, err = db.Exec(ctx, `UPDATE notebook_research_jobs SET lease_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, foreign.ID)
	require.NoError(t, err)
	expired, err := repo.FailExpired(ctx, time.Now(), json.RawMessage(`{"code":"NOTEBOOK_JOB_INTERRUPTED"}`))
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, foreign.ID, expired[0].ID)
	require.Equal(t, NotebookJobFailed, expired[0].State)
	deleted, err := repo.DeleteFinishedBefore(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 3, deleted)

	_, err = repo.FinishJob(ctx, first.ID, NotebookJobCancelled, "", nil, nil)
	require.Error(t, err)
}

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
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestKanbanCardLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "kanban_card_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `
	CREATE TABLE boards (id UUID PRIMARY KEY, task_key_prefix TEXT, task_sequence INT DEFAULT 0, updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ);
	CREATE TABLE tasks (
	 id UUID PRIMARY KEY, board_id UUID NOT NULL REFERENCES boards(id), goal_id UUID, project_id UUID, sprint_id UUID,
	 column_id UUID NOT NULL, created_by BIGINT NOT NULL, task_number INT, task_key TEXT,
	 title TEXT NOT NULL, description TEXT, priority TEXT NOT NULL, due_at TIMESTAMPTZ,
	 original_estimate_sec INT NOT NULL DEFAULT 0, time_spent_sec INT NOT NULL DEFAULT 0,
	 created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, deleted_at TIMESTAMPTZ,
	 parent_id UUID, status TEXT NOT NULL, assignee_id BIGINT NOT NULL);
	CREATE TABLE task_assignees (task_id UUID REFERENCES tasks(id), user_id BIGINT, PRIMARY KEY(task_id,user_id));
	CREATE TABLE task_history (id UUID PRIMARY KEY, task_id UUID REFERENCES tasks(id), user_id BIGINT,
	 action TEXT, field_name TEXT, old_value TEXT, new_value TEXT, metadata JSONB, created_at TIMESTAMPTZ);
	`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260924000100_allow_unassigned_tasks.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	boardID := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id, task_key_prefix) VALUES ($1,'TEST')`, boardID)
	require.NoError(t, err)
	r := NewTaskRepository(db, zap.NewNop())
	description := "Original"
	due := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	task := &domain.Task{BoardID: boardID, ColumnID: uuid.New(), CreatedBy: 1, Title: "Task",
		Description: &description, Priority: "medium", DueAt: &due, Status: domain.StatusTodo, AssigneeIDs: []int64{2}}
	require.NoError(t, r.Create(ctx, task))
	var patch domain.UpdateTaskPatch
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Edited","description":"Updated","priority":"high","assignee_ids":[3,4],"due_at":"2026-11-01T12:00:00Z"}`), &patch))
	require.NoError(t, r.Update(ctx, 1, task.ID, &patch))
	got, err := r.GetByID(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "Edited", got.Title)
	require.Equal(t, "Updated", *got.Description)
	require.Equal(t, "high", got.Priority)
	require.Equal(t, int64(1), got.CreatedBy)
	require.Equal(t, []int64{3, 4}, got.AssigneeIDs)
	require.Equal(t, 11, int(got.DueAt.Month()))
	// Clearing must work alone, without any parameterized SET fields.
	for _, body := range []string{`{"due_at":null}`, `{"description":null}`, `{"assignee_ids":[]}`} {
		require.NoError(t, json.Unmarshal([]byte(body), &patch))
		require.NoError(t, r.Update(ctx, 1, task.ID, &patch))
	}
	got, err = r.GetByID(ctx, task.ID)
	require.NoError(t, err)
	require.Nil(t, got.DueAt)
	require.Nil(t, got.Description)
	require.Empty(t, got.AssigneeIDs)
	require.Equal(t, "Edited", got.Title)
	var legacyID *int64
	require.NoError(t, db.QueryRow(ctx, `SELECT assignee_id FROM tasks WHERE id=$1`, task.ID).Scan(&legacyID))
	require.Nil(t, legacyID)
	var clears int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM task_history WHERE task_id=$1 AND field_name IN ('due_at','description') AND new_value IS NULL`, task.ID).Scan(&clears))
	require.Equal(t, 2, clears)
	unassigned := &domain.Task{BoardID: boardID, ColumnID: task.ColumnID, CreatedBy: 1, Title: "Unassigned", Priority: "medium", Status: domain.StatusTodo}
	require.NoError(t, r.Create(ctx, unassigned))
	require.NoError(t, r.Delete(ctx, 1, task.ID))
	_, err = r.GetByID(ctx, task.ID)
	require.Error(t, err)
}

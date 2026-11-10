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
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestKanbanPersonalIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "kanban_personal_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	CREATE TABLE users(id BIGINT PRIMARY KEY);
	INSERT INTO users VALUES(1),(2),(3);
	CREATE TABLE boards(id UUID PRIMARY KEY,owner_id BIGINT,deleted_at TIMESTAMPTZ);
	CREATE TABLE board_members(board_id UUID,user_id BIGINT);
	CREATE TABLE kanban_columns(id UUID PRIMARY KEY, key TEXT);
	CREATE TABLE tasks(id UUID PRIMARY KEY,board_id UUID,goal_id UUID,project_id UUID,sprint_id UUID,column_id UUID,created_by BIGINT,
	 task_number INT DEFAULT 1,task_key TEXT DEFAULT 'AB-1',title TEXT DEFAULT 'Task',description TEXT,priority TEXT DEFAULT 'medium',due_at TIMESTAMPTZ,
	 original_estimate_sec INT DEFAULT 0,time_spent_sec INT DEFAULT 0,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ,parent_id UUID,status TEXT DEFAULT 'К ВЫПОЛНЕНИЮ');
	CREATE TABLE task_assignees(task_id UUID,user_id BIGINT,PRIMARY KEY(task_id,user_id));
	`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260924000200_kanban_deadline_deliveries.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	board, col, task, done, other, project := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id)VALUES($1,1)`, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO board_members VALUES($1,2)`, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO kanban_columns VALUES($1,'TODO')`, col)
	require.NoError(t, err)
	due := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	for _, id := range []uuid.UUID{task, done, other} {
		_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by,project_id,due_at) VALUES($1,$2,$3,1,$4,$5)`, id, board, col, project, due)
		require.NoError(t, err)
		user := int64(2)
		if id == other {
			user = 3
		}
		_, err = db.Exec(ctx, `INSERT INTO task_assignees VALUES($1,$2)`, id, user)
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `UPDATE tasks SET status='ГОТОВО' WHERE id=$1`, done)
	require.NoError(t, err)
	r := NewTaskRepository(db, zap.NewNop())
	tasks, err := r.ListMyTasks(ctx, 2, domain.MyTasksFilter{Limit: 50, ProjectID: &project})
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	tasks, err = r.ListMyTasks(ctx, 1, domain.MyTasksFilter{Limit: 50})
	require.NoError(t, err)
	require.Empty(t, tasks, "creator is not automatically an assignee")
	tasks, err = r.ListMyTasks(ctx, 3, domain.MyTasksFilter{Limit: 50})
	require.NoError(t, err)
	require.Empty(t, tasks, "assignment does not bypass board access")
	items, err := r.ListTaskDeadlineReminders(ctx, time.Now())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, task, items[0].TaskID)
	token := uuid.New()
	claimed, err := r.ClaimTaskDeadlineReminder(ctx, items[0], token)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = r.ClaimTaskDeadlineReminder(ctx, items[0], uuid.New())
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, r.FinishTaskDeadlineReminder(ctx, items[0], token, false))
	_, err = db.Exec(ctx, `UPDATE kanban_deadline_deliveries SET lease_until=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	token = uuid.New()
	claimed, err = r.ClaimTaskDeadlineReminder(ctx, items[0], token)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, r.FinishTaskDeadlineReminder(ctx, items[0], token, true))
	items, err = r.ListTaskDeadlineReminders(ctx, time.Now())
	require.NoError(t, err)
	require.Empty(t, items)
	_, err = db.Exec(ctx, `UPDATE tasks SET due_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, task)
	require.NoError(t, err)
	items, err = r.ListTaskDeadlineReminders(ctx, time.Now())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "overdue", items[0].Kind)
	tasks, err = r.ListMyTasks(ctx, 2, domain.MyTasksFilter{Limit: 50, Overdue: true})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	_, err = db.Exec(ctx, `DELETE FROM task_assignees WHERE task_id=$1`, task)
	require.NoError(t, err)
	claimed, err = r.ClaimTaskDeadlineReminder(ctx, items[0], uuid.New())
	require.NoError(t, err)
	require.False(t, claimed, "removed assignee cannot receive stale reminder")
}

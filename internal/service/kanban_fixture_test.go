package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
)

func newKanbanAuditFixture(t *testing.T) (context.Context, *pgxpool.Pool, *KanbanService, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "roles_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `
	 CREATE TABLE users(id BIGINT PRIMARY KEY); INSERT INTO users VALUES(1),(2);
	 CREATE TABLE boards(id UUID PRIMARY KEY,owner_id BIGINT,name TEXT DEFAULT 'Board',task_key_prefix TEXT DEFAULT 'AB',task_sequence INT DEFAULT 0,
	 background_mode TEXT DEFAULT 'preset_color',background_preset_id TEXT DEFAULT 'preset_default',background_image_url TEXT,
	 created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
	 CREATE TABLE board_members(board_id UUID,user_id BIGINT,role TEXT);
	 CREATE TABLE tasks(id UUID PRIMARY KEY,board_id UUID,goal_id UUID,project_id UUID,sprint_id UUID,column_id UUID,created_by BIGINT,
	 task_number INT DEFAULT 1,task_key TEXT DEFAULT 'AB-1',title TEXT DEFAULT 'Task',description TEXT,priority TEXT DEFAULT 'medium',due_at TIMESTAMPTZ,
	 original_estimate_sec INT DEFAULT 0,time_spent_sec INT DEFAULT 0,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ,parent_id UUID,status TEXT DEFAULT 'К ВЫПОЛНЕНИЮ');
	 CREATE TABLE task_assignees(task_id UUID,user_id BIGINT);
	 CREATE TABLE task_attachments(id UUID PRIMARY KEY,task_id UUID,uploaded_by BIGINT,created_at TIMESTAMPTZ DEFAULT NOW(),file_name TEXT DEFAULT 'file.txt',object_name TEXT DEFAULT 'secret',file_size BIGINT DEFAULT 1,mime_type TEXT DEFAULT 'text/plain');
	 CREATE TABLE task_comments(id UUID PRIMARY KEY,task_id UUID,user_id BIGINT,content TEXT DEFAULT 'Original',attachment_url TEXT,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
	`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE TABLE task_history(id UUID PRIMARY KEY,task_id UUID,user_id BIGINT,action TEXT,field_name TEXT,old_value TEXT,new_value TEXT,metadata JSONB,created_at TIMESTAMPTZ)`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260924000300_task_checklist_items.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)

	_, err = db.Exec(ctx, `ALTER TABLE users ADD COLUMN username TEXT DEFAULT 'user',ADD COLUMN first_name TEXT,ADD COLUMN last_name TEXT,ADD COLUMN avatar_url TEXT;
 INSERT INTO users(id) VALUES(3),(4),(5),(6);
 ALTER TABLE board_members ADD COLUMN team_role TEXT,ADD COLUMN joined_at TIMESTAMPTZ DEFAULT NOW(),ADD PRIMARY KEY(board_id,user_id);
 ALTER TABLE tasks ADD COLUMN assignee_id BIGINT, ADD COLUMN source_system TEXT, ADD COLUMN source_assignment_id TEXT;
 ALTER TABLE task_attachments ADD COLUMN url TEXT,ADD COLUMN size_bytes BIGINT;
 ALTER TABLE task_history ADD CONSTRAINT test_history_task_fk FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE;
 ALTER TABLE task_history ADD CONSTRAINT test_history_user_fk FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE;
 CREATE TABLE kanban_columns(id UUID PRIMARY KEY,board_id UUID,user_id BIGINT DEFAULT 1,key TEXT,name TEXT,position INT DEFAULT 1,is_system BOOLEAN DEFAULT false,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE projects(id UUID PRIMARY KEY,owner_id BIGINT,board_id UUID,goal_id UUID,name TEXT,description TEXT,status TEXT,created_by BIGINT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE goals(id UUID PRIMARY KEY,board_id UUID,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE kanban_labels(id UUID PRIMARY KEY,board_id UUID,name TEXT,color TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE sprints(id UUID PRIMARY KEY,board_id UUID,project_id UUID,goal_id UUID,status TEXT,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE kanban_task_labels(task_id UUID,label_id UUID,assigned_at TIMESTAMPTZ,PRIMARY KEY(task_id,label_id));
 CREATE TABLE task_worklogs(id UUID PRIMARY KEY,task_id UUID,user_id BIGINT,time_spent_sec INT,started_at TIMESTAMPTZ,comment TEXT,created_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	migration, err = os.ReadFile("../../db/migrations/20260925000300_reconcile_kanban_audit.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	board, col, doneCol, task := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id)VALUES($1,1)`, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO board_members(board_id,user_id,role)VALUES($1,1,'owner'),($1,2,'viewer'),($1,3,'editor'),($1,4,'admin'),($1,5,'member')`, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO kanban_columns(id,board_id,key,name)VALUES($1,$2,'TODO','To Do'),($3,$2,'DONE','Done')`, col, board, doneCol)
	require.NoError(t, err)
	// Viewer remains the original author to exercise the removed creator bypass.
	_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by)VALUES($1,$2,$3,2)`, task, board, col)
	require.NoError(t, err)
	s := &KanbanService{taskRepo: repository.NewTaskRepository(db, zap.NewNop()), boardRepo: repository.NewBoardRepository(db, zap.NewNop()), logger: zap.NewNop()}

	return ctx, db, s, board, col, doneCol, task
}

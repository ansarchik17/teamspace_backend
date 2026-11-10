package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestTaskContentAccessIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "content_access_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	board, otherBoard, task, otherTask, file, comment := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id)VALUES($1,1),($2,2)`, board, otherBoard)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by)VALUES($1,$2,$3,1),($4,$5,$3,2)`, task, board, uuid.New(), otherTask, otherBoard)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_attachments(id,task_id,uploaded_by)VALUES($1,$2,2)`, file, otherTask)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_comments(id,task_id,user_id)VALUES($1,$2,2)`, comment, task)
	require.NoError(t, err)
	s := &KanbanService{taskRepo: repository.NewTaskRepository(db, zap.NewNop()), boardRepo: repository.NewBoardRepository(db, zap.NewNop()), logger: zap.NewNop()}
	_, err = s.GetTaskFileContent(ctx, 1, task, file)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	err = s.DeleteTaskFile(ctx, 1, task, file)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.taskRepo.GetAttachmentByID(ctx, file)
	require.NoError(t, err, "unrelated attachment must remain untouched")
	err = s.UpdateComment(ctx, 2, comment, UpdateCommentRequest{Content: "Changed"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode, "author without board access cannot edit")
	err = s.DeleteComment(ctx, 2, comment)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateChecklistItem(ctx, 2, task, domain.CreateTaskChecklistItem{Title: "No access"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	item, err := s.CreateChecklistItem(ctx, 1, task, domain.CreateTaskChecklistItem{Title: "Valid"})
	require.NoError(t, err)
	_, err = s.ListChecklist(ctx, 2, task)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `INSERT INTO board_members VALUES($1,2,'editor')`, board)
	require.NoError(t, err)
	done := true
	_, err = s.UpdateChecklistItem(ctx, 2, task, item.ID, domain.UpdateTaskChecklistItem{IsCompleted: &done})
	require.NoError(t, err)
	err = s.UpdateComment(ctx, 2, comment, UpdateCommentRequest{Content: "Changed"})
	require.NoError(t, err)
	err = s.DeleteComment(ctx, 2, comment)
	require.NoError(t, err)
}

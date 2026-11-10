package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestTaskContentIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "task_content_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	 CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT,first_name TEXT,last_name TEXT,avatar_url TEXT);
	 INSERT INTO users VALUES(1,'author','Test','User',NULL);
	 CREATE TABLE tasks(id UUID PRIMARY KEY,deleted_at TIMESTAMPTZ);
	 CREATE TABLE task_comments(id UUID PRIMARY KEY,task_id UUID REFERENCES tasks(id),user_id BIGINT REFERENCES users(id),
	 content TEXT,attachment_url TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
	 CREATE TABLE task_attachments(id UUID PRIMARY KEY,task_id UUID REFERENCES tasks(id),uploaded_by BIGINT,created_at TIMESTAMPTZ,
	 file_name TEXT,object_name TEXT,file_size BIGINT,mime_type TEXT,url TEXT,size_bytes BIGINT);
	`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE TABLE task_history(id UUID PRIMARY KEY,task_id UUID,user_id BIGINT,action TEXT,field_name TEXT,old_value TEXT,new_value TEXT,metadata JSONB,created_at TIMESTAMPTZ)`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260924000300_task_checklist_items.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	taskID, otherID := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO tasks(id)VALUES($1),($2)`, taskID, otherID)
	require.NoError(t, err)
	r := NewTaskRepository(db, zap.NewNop())
	items, err := r.ListChecklist(ctx, taskID)
	require.NoError(t, err)
	require.NotNil(t, items)
	require.Empty(t, items)
	item, err := r.CreateChecklistItem(ctx, taskID, 1, domain.CreateTaskChecklistItem{Title: "Check first"})
	require.NoError(t, err)
	second, err := r.CreateChecklistItem(ctx, taskID, 1, domain.CreateTaskChecklistItem{Title: "Check second"})
	require.NoError(t, err)
	require.Greater(t, second.Position, item.Position)
	done := true
	title := "Edited"
	pos := 10
	item, err = r.UpdateChecklistItem(ctx, taskID, item.ID, 1, domain.UpdateTaskChecklistItem{Title: &title, IsCompleted: &done, Position: &pos})
	require.NoError(t, err)
	require.True(t, item.IsCompleted)
	require.Equal(t, "Edited", item.Title)
	items, err = r.ListChecklist(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, second.ID, items[0].ID)
	done = false
	item, err = r.UpdateChecklistItem(ctx, taskID, item.ID, 1, domain.UpdateTaskChecklistItem{IsCompleted: &done})
	require.NoError(t, err)
	require.False(t, item.IsCompleted)
	_, err = r.UpdateChecklistItem(ctx, otherID, item.ID, 1, domain.UpdateTaskChecklistItem{IsCompleted: &done})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.ErrorIs(t, r.DeleteChecklistItem(ctx, otherID, item.ID, 1), pgx.ErrNoRows)
	require.NoError(t, r.DeleteChecklistItem(ctx, taskID, item.ID, 1))
	require.ErrorIs(t, r.DeleteChecklistItem(ctx, taskID, item.ID, 1), pgx.ErrNoRows)
	com := &domain.TaskComment{ID: uuid.New(), TaskID: taskID, UserID: 1, Content: "Hello", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, r.CreateComment(ctx, com))
	comments, err := r.GetComments(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, "author", comments[0].Author.Username)
	require.NoError(t, r.UpdateComment(ctx, com.ID, 1, "Edited comment"))
	got, err := r.GetCommentByID(ctx, com.ID)
	require.NoError(t, err)
	require.Equal(t, "Edited comment", got.Content)
	require.NoError(t, r.DeleteComment(ctx, com.ID, 1))
	_, err = r.GetCommentByID(ctx, com.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	comments, err = r.GetComments(ctx, taskID)
	require.NoError(t, err)
	require.NotNil(t, comments)
	require.Empty(t, comments)
	att := &domain.TaskAttachment{TaskID: taskID, UploadedBy: 1, FileName: "report.txt", ObjectName: "tasks/unique/report.txt", FileSize: 5, MimeType: "text/plain"}
	require.NoError(t, r.AddAttachment(ctx, att))
	files, err := r.GetAttachments(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Contains(t, files[0].DownloadURL, att.ID.String())
	file, err := r.GetAttachmentByID(ctx, att.ID)
	require.NoError(t, err)
	require.Equal(t, taskID, file.TaskID)
	require.NoError(t, r.DeleteAttachment(ctx, att.ID, 1))
	_, err = r.GetAttachmentByID(ctx, att.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = db.Exec(ctx, `UPDATE tasks SET deleted_at=NOW() WHERE id=$1`, taskID)
	require.NoError(t, err)
	_, err = r.CreateChecklistItem(ctx, taskID, 1, domain.CreateTaskChecklistItem{Title: "Cannot create"})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

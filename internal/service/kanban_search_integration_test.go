package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestKanbanSearchIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "kanban_search_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	// pg_trgm is installed once in public by CI, while fixture tables stay private.
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
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

	_, err = db.Exec(ctx, `CREATE TABLE kanban_columns(id UUID PRIMARY KEY,board_id UUID,key TEXT,name TEXT,position INT DEFAULT 1,is_system BOOLEAN DEFAULT true,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE kanban_labels(id UUID PRIMARY KEY,board_id UUID,name TEXT,color TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE kanban_task_labels(task_id UUID,label_id UUID);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260924000400_task_search_indexes.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	board, privateBoard, col, doneCol, sprint := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id) VALUES($1,1),($2,2)`, board, privateBoard)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO board_members VALUES($1,1,'owner'),($2,2,'owner')`, board, privateBoard)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO kanban_columns(id,board_id,key,name) VALUES($1,$2,'TODO','To Do'),($3,$2,'DONE','Done')`, col, board, doneCol)
	require.NoError(t, err)
	first, second, third, secret, deleted := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{first, second, third, secret, deleted} {
		b, c, author, status := board, col, int64(1), domain.StatusTodo
		if i == 1 {
			c = doneCol
			status = domain.StatusDone
		}
		if i == 2 {
			author = 2
		}
		if i == 3 {
			b = privateBoard
		}
		_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by,title,description,status,sprint_id,due_at) VALUES($1,$2,$3,$4,'Отчёт 100%_ready','search description',$5,$6,'2026-10-01T12:00:00Z')`, id, b, c, author, status, sprint)
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `UPDATE tasks SET deleted_at=NOW() WHERE id=$1`, deleted)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_assignees VALUES($1,1),($1,2);`, first)
	require.NoError(t, err)
	attachment, label := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO task_attachments(id,task_id,uploaded_by)VALUES($1,$2,1)`, attachment, first)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO kanban_labels(id,board_id,name,color)VALUES($1,$2,'Release','blue')`, label, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO kanban_task_labels VALUES($1,$2)`, first, label)
	require.NoError(t, err)
	s := &KanbanService{taskRepo: repository.NewTaskRepository(db, zap.NewNop()), boardRepo: repository.NewBoardRepository(db, zap.NewNop()), logger: zap.NewNop()}
	boardStr := board.String()
	req := SearchTasksRequest{BoardID: &boardStr, AuthorIDs: []int64{1}, Statuses: []string{"todo"}, Query: "отчёт", Limit: 20}
	tasks, err := s.SearchTasks(ctx, 1, req)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, first, tasks[0].ID)
	require.Equal(t, []int64{1, 2}, tasks[0].AssigneeIDs)
	require.Len(t, tasks[0].Attachments, 1)
	require.Equal(t, attachment, tasks[0].Attachments[0].ID)
	require.Len(t, tasks[0].Labels, 1)
	view, err := s.GetBoardWithFilters(ctx, 1, &GetBoardFilters{SearchTasksRequest: req})
	require.NoError(t, err)
	require.Equal(t, board, view.Board.ID)
	require.Len(t, view.Columns, 2)
	var ids []uuid.UUID
	for _, c := range view.Columns {
		for _, task := range c.Tasks {
			ids = append(ids, task.ID)
		}
	}
	require.Equal(t, []uuid.UUID{first}, ids)
	for _, q := range []string{"100%_", "search description", "ab-1"} {
		tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{Query: q})
		require.NoError(t, err)
		require.Len(t, tasks, 3)
	}
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{Query: "100%_missing"})
	require.NoError(t, err)
	require.Empty(t, tasks)
	require.NotNil(t, tasks)
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{AssigneeIDs: []int64{1, 2}})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{Statuses: []string{"ГОТОВО"}})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, second, tasks[0].ID)
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{AuthorIDs: []int64{2}})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, third, tasks[0].ID)
	page := SearchTasksRequest{SprintIDs: []string{sprint.String()}, Limit: 1}
	one, err := s.SearchTasks(ctx, 1, page)
	require.NoError(t, err)
	require.Len(t, one, 1)
	page.Offset = 1
	two, err := s.SearchTasks(ctx, 1, page)
	require.NoError(t, err)
	require.Len(t, two, 1)
	require.NotEqual(t, one[0].ID, two[0].ID)
	privateStr := privateBoard.String()
	_, err = s.GetBoardWithFilters(ctx, 1, &GetBoardFilters{SearchTasksRequest: SearchTasksRequest{BoardID: &privateStr}})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	owner := int64(2)
	_, err = s.GetBoardWithFilters(ctx, 1, &GetBoardFilters{UserID: &owner})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE tasks SET due_at='2026-10-03T12:00:00Z' WHERE id=$1`, first)
	require.NoError(t, err)
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	view, err = s.GetBoardWithFilters(ctx, 1, &GetBoardFilters{SearchTasksRequest: req, Date: &day})
	require.NoError(t, err)
	for _, c := range view.Columns {
		require.Empty(t, c.Tasks)
	}
	_, err = db.Exec(ctx, `INSERT INTO board_members VALUES($1,1,'member')`, privateBoard)
	require.NoError(t, err)
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{})
	require.NoError(t, err)
	require.Len(t, tasks, 4)
	_, err = db.Exec(ctx, `UPDATE boards SET deleted_at=NOW() WHERE id=$1`, privateBoard)
	require.NoError(t, err)
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{})
	require.NoError(t, err)
	require.Len(t, tasks, 3)
	// Exercise the real search with a larger dataset; no machine-dependent timing assertion.
	_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by,title) SELECT md5('search-fixture-'||n::text)::uuid,$1,$2,1,'Background task '||n FROM generate_series(1,20000)n`, board, col)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `ANALYZE tasks`)
	require.NoError(t, err)
	started := time.Now()
	tasks, err = s.SearchTasks(ctx, 1, SearchTasksRequest{Query: "отчёт", BoardID: &boardStr})
	require.NoError(t, err)
	require.Len(t, tasks, 3)
	t.Logf("search of 20,003 visible tasks: %s", time.Since(started))

}

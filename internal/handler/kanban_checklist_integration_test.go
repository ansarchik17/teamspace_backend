package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/service"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestChecklistRoutesIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "checklist_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

	board, task := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id) VALUES($1,1)`, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by)VALUES($1,$2,$3,1)`, task, board, uuid.New())
	require.NoError(t, err)
	svc := service.NewKanbanService(repository.NewBoardRepository(db, zap.NewNop()), repository.NewTaskRepository(db, zap.NewNop()), nil, nil, nil, nil, nil, nil, zap.NewNop())
	h := &KanbanHandler{svc: svc, logger: zap.NewNop()}
	router := gin.New()
	userID := int64(1)
	group := router.Group("/api/v1/kanban", func(c *gin.Context) { c.Set("user_id", userID) })
	h.RegisterChecklistRoutes(group)
	call := func(method, path, body string, expected int) *httptest.ResponseRecorder {
		url := "/api/v1/kanban/tasks/" + task.String() + "/" + path
		if strings.HasPrefix(path, "/") {
			url = "/api/v1/kanban" + path
		}
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		if method != "GET" {
			req.Header.Set("Content-Type", "application/json")
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		require.Equal(t, expected, out.Code, out.Body.String())
		return out
	}
	for _, path := range []string{"checklist", "checklist-items"} {
		out := call("GET", path, "", 200)
		require.JSONEq(t, "[]", out.Body.String())
		out = call("POST", path, `{"title":"Проверить отчёт"}`, 201)
		var item domain.TaskChecklistItem
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &item))
		require.Equal(t, "Проверить отчёт", item.Title)
		for _, readPath := range []string{"checklist", "checklist-items"} {
			out = call("GET", readPath, "", 200)
			var items []domain.TaskChecklistItem
			require.NoError(t, json.Unmarshal(out.Body.Bytes(), &items))
			require.Len(t, items, 1)
			require.Equal(t, item.ID, items[0].ID)
			// A GET never invokes JSON validation, even when sent an irrelevant body.
			call("GET", readPath, "invalid JSON", 200)
			call("PATCH", readPath+"/"+item.ID.String(), `{"is_completed":true}`, 200)
			out = call("PUT", readPath+"/"+item.ID.String(), `{"is_completed":false}`, 200)
			require.NoError(t, json.Unmarshal(out.Body.Bytes(), &item))
			require.False(t, item.IsCompleted)
		}
		call("POST", path, "", 400)
		call("POST", path, `{"title":""}`, 400)
		call("DELETE", path+"/"+item.ID.String(), "", 200)
		out = call("GET", path, "", 200)
		require.JSONEq(t, "[]", out.Body.String())
	}
	out := call("POST", "checklist", `{"text":"Frontend item","completed":true}`, 201)
	var created struct {
		ID          string `json:"id"`
		Text        string `json:"text"`
		Title       string `json:"title"`
		Completed   bool   `json:"completed"`
		IsCompleted bool   `json:"is_completed"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &created))
	require.Equal(t, "Frontend item", created.Text)
	require.Equal(t, created.Title, created.Text)
	require.True(t, created.Completed)
	require.True(t, created.IsCompleted)
	short := "/checklist-items/" + created.ID
	out = call("PATCH", short, `{"text":"Updated","completed":false}`, 200)
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &created))
	require.Equal(t, "Updated", created.Title)
	require.False(t, created.Completed)
	require.False(t, created.IsCompleted)
	call("PATCH", short, `{"title":"A","text":"B"}`, 400)
	call("PATCH", short, `{"is_completed":true,"completed":false}`, 400)
	call("POST", "checklist", `{"text":" "}`, 400)
	userID = 999
	call("PATCH", short, `{"completed":true}`, 403)
	call("DELETE", short, "", 403)
	userID = 1
	call("DELETE", short, "", 200)
	call("PATCH", short, `{"completed":true}`, 404)
	out = call("GET", "checklist", "", 200)
	require.JSONEq(t, "[]", out.Body.String())

}

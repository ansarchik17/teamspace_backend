package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAIChatSectionsArchiveAndOwnerIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "ai_sections_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY);INSERT INTO users VALUES(1),(2)`)
	require.NoError(t, err)
	for _, name := range []string{"026_create_ai_tables.sql", "20261022100300_ai_chat_sections_archive.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	s := service.NewAlemService(&config.Config{}, repository.NewAIRepository(db, zap.NewNop()), nil, nil, zap.NewNop())
	h := NewAlemHandler(s, zap.NewNop())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.GetHeader("X-Test-User"), 10, 64)
		c.Set("user_id", id)
	})
	r.GET("/ai/chats", h.GetUserChats)
	r.POST("/ai/chats", h.CreateChat)
	r.PATCH("/ai/chats/:id", h.PatchChat)
	r.GET("/ai/sections", h.ListChatSections)
	r.POST("/ai/sections", h.CreateChatSection)
	r.PATCH("/ai/sections/:sectionId", h.UpdateChatSection)
	r.DELETE("/ai/sections/:sectionId", h.DeleteChatSection)
	call := func(user int, method, path, body string, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", strconv.Itoa(user))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
		return w.Body.Bytes()
	}
	decode := func(data []byte) map[string]any {
		t.Helper()
		var result map[string]any
		require.NoError(t, json.Unmarshal(data, &result))
		return result
	}
	own := decode(call(1, "POST", "/ai/sections", `{"name":"Работа","description":"Задачи","color":"#2458D3"}`, 201))["id"].(string)
	other := decode(call(2, "POST", "/ai/sections", `{"name":"Private"}`, 201))["id"].(string)
	call(1, "POST", "/ai/sections", `{"name":" ","color":"invalid"}`, 400)
	chat := decode(call(1, "POST", "/ai/chats", `{"title":"In section","section_id":"`+own+`"}`, 201))["id"].(string)
	call(2, "POST", "/ai/chats", `{"title":"Cross owner","section_id":"`+own+`"}`, 404)
	call(2, "PATCH", "/ai/chats/"+chat, `{"archived":true}`, 404)
	call(1, "PATCH", "/ai/chats/"+chat, `{"title":"Must not change","section_id":"`+other+`"}`, 404)
	call(2, "PATCH", "/ai/sections/"+own, `{"name":"Hijacked"}`, 404)
	call(2, "DELETE", "/ai/sections/"+own, "", 404)
	patched := decode(call(1, "PATCH", "/ai/chats/"+chat, `{"title":"Renamed","pinned":true,"archived":true}`, 200))
	require.Equal(t, true, patched["archived"])
	require.Equal(t, true, patched["pinned"])
	require.NotNil(t, patched["archived_at"])
	require.JSONEq(t, "[]", string(call(1, "GET", "/ai/chats", "", 200)))
	var archived []map[string]any
	require.NoError(t, json.Unmarshal(call(1, "GET", "/ai/chats?include_archived=true", "", 200), &archived))
	require.Len(t, archived, 1)
	require.Equal(t, own, archived[0]["section_id"])
	restored := decode(call(1, "PATCH", "/ai/chats/"+chat, `{"archived":false}`, 200))
	require.Nil(t, restored["archived_at"])
	_, err = db.Exec(ctx, `INSERT INTO ai_messages(conversation_id,role,content) VALUES($1,'user','History stays')`, chat)
	require.NoError(t, err)
	call(1, "PATCH", "/ai/sections/"+own, `{"name":"Updated","color":"#F97316"}`, 200)
	call(1, "PATCH", "/ai/chats/"+chat, `{"section_id":null}`, 200)
	call(1, "PATCH", "/ai/chats/"+chat, `{"section_id":"`+own+`"}`, 200)
	call(1, "DELETE", "/ai/sections/"+own, "", 204)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_messages WHERE conversation_id=$1`, chat).Scan(&count))
	require.Equal(t, 1, count)
	var section *string
	require.NoError(t, db.QueryRow(ctx, `SELECT section_id FROM ai_conversations WHERE id=$1`, chat).Scan(&section))
	require.Nil(t, section)
	require.JSONEq(t, "[]", string(call(2, "GET", "/ai/chats?include_archived=true", "", 200)))
	// A simultaneous move and section deletion must never leave a dangling
	// assignment or expose a foreign section. A deleted target returns 404.
	for range 5 {
		target := decode(call(1, "POST", "/ai/sections", `{"name":"Concurrent target"}`, 201))["id"].(string)
		start := make(chan struct{})
		results := make(chan int, 2)
		for _, method := range []string{"PATCH", "DELETE"} {
			go func(method string) {
				<-start
				path, body := "/ai/sections/"+target, ""
				if method == "PATCH" {
					path = "/ai/chats/" + chat
					body = `{"section_id":"` + target + `"}`
				}
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Test-User", "1")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				results <- w.Code
			}(method)
		}
		close(start)
		a, b := <-results, <-results
		require.Contains(t, []int{200, 204, 404}, a)
		require.Contains(t, []int{200, 204, 404}, b)
		require.NoError(t, db.QueryRow(ctx, `SELECT section_id FROM ai_conversations WHERE id=$1`, chat).Scan(&section))
		require.Nil(t, section)
	}
	// Database ownership protection also prevents bypassing service validation.
	_, err = db.Exec(ctx, `UPDATE ai_conversations SET section_id=$2 WHERE id=$1`, chat, other)
	require.Error(t, err)
}

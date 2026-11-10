package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/handler"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestMeetingResultRejectsInvalidInput(t *testing.T) {
	h := handler.NewNoteHandler(service.NewNoteService(nil, nil, zap.NewNop()), zap.NewNop())
	r := newTestRouter()
	r.POST("/result", h.SaveMeetingNote)
	for _, body := range []string{`{}`, `{"job_id":"  "}`, `{"job_id":"job"}`, `{"job_id":"job","content_doc":{"blocks":[{"type":"paragraph","runs":[{"text":" "}]}]}}`, `{"job_id":"` + strings.Repeat("x", 201) + `"}`, `{"job_id":"job","content":"` + strings.Repeat("x", 4<<20) + `"}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/result", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Contains(t, []int{400, 422}, w.Code)
	}
}

// Uses a fresh schema and real migrations. No production tables are touched.
func TestMeetingResultPersistence(t *testing.T) {
	dsn := os.Getenv("TEST_NOTES_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_NOTES_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "meeting_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, "CREATE TABLE users (id BIGINT PRIMARY KEY); CREATE TABLE files (id UUID PRIMARY KEY); INSERT INTO users VALUES (1), (2)")
	require.NoError(t, err)
	for _, migration := range []string{"20260323000100_create_notes_module.sql", "20260325000100_notes_rich_content.sql", "20260325000200_fix_notes_type_check.sql", "20260921000100_add_color_to_note_folders.sql", "20260922000100_note_meeting_result.sql"} {
		raw, err := os.ReadFile("../../db/migrations/" + migration)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0])
		require.NoError(t, err, migration)
	}
	svc := service.NewNoteService(repository.NewNoteRepository(pool, zap.NewNop()), nil, zap.NewNop())
	h := handler.NewNoteHandler(svc, zap.NewNop())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		var id int64 = 1
		if c.GetHeader("X-Test-User") == "2" {
			id = 2
		}
		c.Set("user_id", id)
	})
	r.POST("/result", h.SaveMeetingNote)
	request := func(user string, extra string) *httptest.ResponseRecorder {
		body := `{"job_id":"provider-job-1","title":"Meeting","type":"rich","is_pinned":true,"content_doc":{"type":"doc","version":1,"blocks":[{"id":"p","type":"paragraph","runs":[{"text":"Summary and tasks"}]}]}` + extra + `}`
		req := httptest.NewRequest(http.MethodPost, "/result", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", user)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 12)
	for i := range responses {
		wg.Add(1)
		go func(i int) { defer wg.Done(); responses[i] = request("1", "") }(i)
	}
	wg.Wait()
	var saved domain.Note
	for _, w := range responses {
		require.Equal(t, 200, w.Code, w.Body.String())
		var note domain.Note
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &note))
		if saved.ID != uuid.Nil {
			require.Equal(t, saved.ID, note.ID)
		}
		saved = note
	}
	require.True(t, saved.IsPinned)
	require.Contains(t, saved.ContentText, "Summary and tasks")
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM notes WHERE user_id=1").Scan(&count))
	require.Equal(t, 1, count)
	w := request("2", "")
	require.Equal(t, 200, w.Code)
	var other domain.Note
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &other))
	require.NotEqual(t, saved.ID, other.ID)
	_, err = pool.Exec(ctx, "UPDATE notes SET title='Edited', version=2 WHERE id=$1", saved.ID)
	require.NoError(t, err)
	w = request("1", "")
	require.Equal(t, 200, w.Code)
	var edited domain.Note
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &edited))
	require.Equal(t, "Edited", edited.Title)
	require.Equal(t, 2, edited.Version)
	_, err = pool.Exec(ctx, "UPDATE notes SET is_deleted=true WHERE id=$1", saved.ID)
	require.NoError(t, err)
	require.Equal(t, 409, request("1", "").Code)
	folderID := uuid.New()
	_, err = pool.Exec(ctx, "INSERT INTO note_folders(id,user_id,name) VALUES($1,2,'Private')", folderID)
	require.NoError(t, err)
	require.Equal(t, 404, request("1", fmt.Sprintf(`,"folder_id":%q`, folderID.String())).Code)
	// Regular notes remain independent and do not collide on the NULL job key.
	for i := 0; i < 2; i++ {
		_, err = svc.CreateNote(ctx, 1, &domain.CreateNoteRequest{Title: "Regular", Type: "text"})
		require.NoError(t, err)
	}
}

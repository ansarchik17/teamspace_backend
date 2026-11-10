package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/handler"
	"go.uber.org/zap"
)

func TestNTS003AutosaveNoteRequest_Binding(t *testing.T) {
	r := newTestRouter()
	r.POST("/notes/autosave", func(c *gin.Context) {
		var req domain.AutosaveNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.ID == nil || *req.ID == uuid.Nil {
			c.JSON(http.StatusCreated, req)
			return
		}
		c.JSON(http.StatusOK, req)
	})

	t.Run("create draft without id", func(t *testing.T) {
		body := map[string]any{
			"title":   "Draft 1",
			"content": "Typing content...",
			"type":    "text",
		}
		req := httptest.NewRequest(http.MethodPost, "/notes/autosave", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp domain.AutosaveNoteRequest
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "Draft 1", resp.Title)
		assert.Nil(t, resp.ID)
	})

	t.Run("update existing note with id and version", func(t *testing.T) {
		noteID := uuid.New()
		body := map[string]any{
			"id":      noteID.String(),
			"title":   "Updated Title",
			"content": "Updated content while typing",
			"version": 4,
		}
		req := httptest.NewRequest(http.MethodPost, "/notes/autosave", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp domain.AutosaveNoteRequest
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.NotNil(t, resp.ID)
		assert.Equal(t, noteID, *resp.ID)
		assert.Equal(t, "Updated Title", resp.Title)
		assert.Equal(t, 4, resp.Version)
	})
}

func TestNTS003AutosavePayloadLimit(t *testing.T) {
	r := newTestRouter()
	h := handler.NewNoteHandler(nil, zap.NewNop())
	r.POST("/notes/autosave", h.AutosaveNote)
	for _, chunked := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/notes/autosave", strings.NewReader(`{"content":"`+strings.Repeat("x", 4<<20)+`"}`))
		if chunked {
			req.ContentLength = -1
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	}
}

func TestNTS003TrashEndpoints_RoutingAndRejections(t *testing.T) {
	h := handler.NewNoteHandler(nil, zap.NewNop())
	router := newTestRouter()

	router.GET("/notes/trash", h.ListTrash)
	router.GET("/notes/trash/count", h.GetTrashCount)
	router.DELETE("/notes/trash", h.EmptyTrash)
	router.POST("/notes/trash/empty", h.EmptyTrash)
	router.POST("/notes/trash/:id/restore", h.RestoreNote)
	router.DELETE("/notes/trash/:id", h.HardDeleteNote)

	t.Run("invalid note id on restore returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/notes/trash/not-a-uuid/restore", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid note id on hard delete returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/notes/trash/not-a-uuid", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestNTS003Note_ExpiresAt_Calculation(t *testing.T) {
	now := time.Now()
	deletedAt := now
	note := domain.Note{
		ID:        uuid.New(),
		UserID:    42,
		Title:     "Trashed",
		IsDeleted: true,
		DeletedAt: &deletedAt,
		ExpiresAt: domain.CalculateTrashExpiresAt(&deletedAt),
	}

	require.NotNil(t, note.ExpiresAt)
	expected := deletedAt.Add(domain.NoteTrashRetention)
	assert.Equal(t, expected.Unix(), note.ExpiresAt.Unix())

	// Test IsTrashExpired
	assert.False(t, domain.IsTrashExpired(note.DeletedAt, now))
	assert.False(t, domain.IsTrashExpired(note.DeletedAt, now.Add(29*24*time.Hour)))
	assert.True(t, domain.IsTrashExpired(note.DeletedAt, now.Add(31*24*time.Hour)))
}

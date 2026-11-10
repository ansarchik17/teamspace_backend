package handler_test

import (
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/handler"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNTS002RealHandlersRejectInvalidParameters(t *testing.T) {
	h := handler.NewNoteHandler(nil, zap.NewNop())
	router := newTestRouter()
	router.GET("/notes/search", h.SearchNotes)
	router.PATCH("/notes/:id/favorite", h.SetFavoriteNote)
	for _, path := range []string{"/notes/search", "/notes/search?q=x&isFavorite=invalid", "/notes/search?q=x&isDeleted=invalid", "/notes/search?q=x&isArchived=invalid", "/notes/search?q=x&folderId=invalid"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
	for _, path := range []string{"/notes/invalid/favorite", "/notes/11111111-1111-1111-1111-111111111111/favorite"} {
		req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"is_favorite":true}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}

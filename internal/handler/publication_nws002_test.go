package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func setupTestNWS002Router(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.POST("/archive/obsolete", pubHandler.ArchiveObsoletePublications)
		pubs.POST("/:id/publish", pubHandler.PublishPublication)
		pubs.POST("/:id/unpublish", pubHandler.UnpublishPublication)
		pubs.POST("/:id/archive", pubHandler.ArchivePublication)
		pubs.POST("/:id/unarchive", pubHandler.UnarchivePublication)
	}

	return r
}

func TestNWS002_HandlerRoutesAndValidation(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestNWS002Router(pubHandler)

	t.Run("archive with invalid UUID returns 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/not-a-valid-uuid/archive", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("unarchive with invalid UUID returns 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/bad-uuid/unarchive", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("unarchive with malformed JSON body returns 400", func(t *testing.T) {
		validID := uuid.New().String()
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/"+validID+"/unarchive", bytes.NewBufferString(`{malformed`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("archive obsolete with malformed JSON body returns 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/archive/obsolete", bytes.NewBufferString(`{invalid json`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("publish with invalid archive_at timestamp returns 400", func(t *testing.T) {
		validID := uuid.New().String()
		body := bytes.NewBufferString(`{"archive_at":"not-a-timestamp"}`)
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/"+validID+"/publish", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for invalid timestamp format, got %d", w.Code)
		}
	})
}

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

func setupTestPublicationRouter(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Dummy user middleware to simulate authenticated user with ID 42
	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.GET("", pubHandler.ListPublications)
		pubs.POST("/:id/publish", pubHandler.PublishPublication)
		pubs.POST("/:id/unpublish", pubHandler.UnpublishPublication)
		pubs.POST("/:id/cover", pubHandler.SetCoverImage)
		pubs.DELETE("/:id/cover", pubHandler.RemoveCoverImage)
	}

	return r
}

func TestNWS001_HandlerValidationErrors(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestPublicationRouter(pubHandler)
	t.Run("invalid publish bodies never reach publication service", func(t *testing.T) {
		for _, body := range []string{`{`, `{"scheduled_at":"invalid"}`} {
			for _, chunked := range []bool{false, true} {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+uuid.New().String()+"/publish", bytes.NewBufferString(body))
				if chunked {
					req.ContentLength = -1
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("invalid body returned %d", w.Code)
				}
			}
		}
	})

	t.Run("publish with invalid UUID returns 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/invalid-uuid/publish", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("unpublish with invalid UUID returns 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/not-a-uuid/unpublish", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("cover update without valid payload returns 400", func(t *testing.T) {
		validID := uuid.New().String()
		body := bytes.NewBufferString(`{}`) // missing cover_image_url
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/publications/"+validID+"/cover", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for missing cover_image_url, got %d", w.Code)
		}
	})

	t.Run("delete cover with invalid UUID returns 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, "/api/v1/publications/not-a-uuid/cover", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})
}

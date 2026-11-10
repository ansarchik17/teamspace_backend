package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func setupTestPublicationNWS007Router(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.POST("/:id/acknowledge", pubHandler.AcknowledgePublication)
		pubs.DELETE("/:id/acknowledge", pubHandler.UnacknowledgePublication)
		pubs.POST("/:id/unacknowledge", pubHandler.UnacknowledgePublication)

		pubs.GET("/:id/analytics", pubHandler.GetPublicationAnalytics)
		pubs.GET("/:id/read-stats", pubHandler.GetPublicationAnalytics)

		pubs.GET("/:id/readers", pubHandler.ListPublicationReaders)
		pubs.GET("/:id/readers/export", pubHandler.ExportPublicationReaders)
		pubs.GET("/:id/export/readers", pubHandler.ExportPublicationReaders)
	}

	return r
}

func TestNWS007_HandlerValidation(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestPublicationNWS007Router(pubHandler)

	t.Run("POST /:id/acknowledge invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-valid-uuid/acknowledge", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("DELETE /:id/acknowledge invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/publications/not-a-valid-uuid/acknowledge", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("POST /:id/unacknowledge invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-valid-uuid/unacknowledge", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GET /:id/analytics invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-valid-uuid/analytics", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GET /:id/read-stats invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-valid-uuid/read-stats", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GET /:id/readers invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-valid-uuid/readers", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GET /:id/readers/export invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-valid-uuid/readers/export", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GET /:id/export/readers invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-valid-uuid/export/readers", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

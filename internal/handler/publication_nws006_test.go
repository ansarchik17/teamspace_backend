package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func setupTestPublicationNWS006Router(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.GET("/banners", pubHandler.ListBanners)
		pubs.POST("/:id/banner/dismiss", pubHandler.DismissBanner)
		pubs.PUT("/:id/banner", pubHandler.SetBanner)
		pubs.DELETE("/:id/banner", pubHandler.RemoveBanner)
		pubs.PUT("/:id/pin", pubHandler.PinPublication)
		pubs.DELETE("/:id/pin", pubHandler.UnpinPublication)
		pubs.POST("/:id/broadcast", pubHandler.BroadcastPublication)
		pubs.GET("/:id/broadcast/stats", pubHandler.GetBroadcastStats)
		pubs.GET("/:id/acknowledgements", pubHandler.ListAcknowledgements)
	}

	return r
}

func TestNWS006_HandlerValidation(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestPublicationNWS006Router(pubHandler)
	pubID := uuid.New().String()

	t.Run("dismiss banner with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-uuid/banner/dismiss", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("set banner with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/not-a-uuid/banner", bytes.NewBufferString(`{"is_banner":true}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("set banner with invalid JSON returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/"+pubID+"/banner", bytes.NewBufferString(`{invalid_json}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("remove banner with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/publications/not-a-uuid/banner", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("pin publication with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/not-a-uuid/pin", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unpin publication with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/publications/not-a-uuid/pin", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("broadcast with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-uuid/broadcast", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("get broadcast stats with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-uuid/broadcast/stats", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list acknowledgements with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/not-a-uuid/acknowledgements", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

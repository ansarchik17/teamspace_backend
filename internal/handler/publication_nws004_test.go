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

func setupTestPublicationNWS004Router(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.POST("", pubHandler.CreatePublication)
		pubs.PUT("/:id", pubHandler.UpdatePublication)
		pubs.GET("", pubHandler.ListPublications)
	}

	return r
}

func TestNWS004_HandlerAudienceValidation(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestPublicationNWS004Router(pubHandler)
	pubID := uuid.New().String()

	t.Run("create publication with invalid target_audience returns 400", func(t *testing.T) {
		body := bytes.NewBufferString(`{
			"source_type": "user",
			"type": "news",
			"title": "Тестовая новость",
			"content": "Текст",
			"target_audience": "unknown_scope",
			"importance": "normal"
		}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("update publication with invalid target_audience returns 400", func(t *testing.T) {
		body := bytes.NewBufferString(`{
			"source_type": "user",
			"type": "news",
			"title": "Обновленная новость",
			"content": "Текст",
			"target_audience": "invalid_audience",
			"importance": "normal"
		}`)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/"+pubID, body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("update publication with invalid UUID returns 400", func(t *testing.T) {
		body := bytes.NewBufferString(`{
			"source_type": "user",
			"type": "news",
			"title": "Обновленная новость",
			"content": "Текст",
			"target_audience": "department",
			"importance": "normal"
		}`)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/not-a-valid-uuid", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

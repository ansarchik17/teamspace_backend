package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func setupTestPublicationMediaRouter(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.POST("/:id/attachments", pubHandler.UploadAttachments)
		pubs.POST("/:id/gallery", pubHandler.UploadGallery)
		pubs.POST("/:id/videos", pubHandler.AddVideoEmbed)
		pubs.DELETE("/:id/attachments/:attachment_id", pubHandler.DeleteAttachment)
		pubs.PUT("/:id/attachments/reorder", pubHandler.ReorderAttachments)
	}

	return r
}

func TestNWS003_HandlerValidation(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestPublicationMediaRouter(pubHandler)

	t.Run("upload attachments with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-uuid/attachments", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("upload attachments non-multipart returns 400", func(t *testing.T) {
		pubID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+pubID+"/attachments", bytes.NewBufferString("{}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("upload gallery with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-uuid/gallery", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("oversized gallery rejected before access or multipart parsing", func(t *testing.T) {
		pubID := uuid.New().String()
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+pubID+"/gallery", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.ContentLength = (100 << 20) + 1
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("add video embed with empty body returns 400", func(t *testing.T) {
		pubID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+pubID+"/videos", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("delete attachment with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/publications/not-a-uuid/attachments/not-a-uuid", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("reorder attachments with empty body returns 400", func(t *testing.T) {
		pubID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/"+pubID+"/attachments/reorder", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

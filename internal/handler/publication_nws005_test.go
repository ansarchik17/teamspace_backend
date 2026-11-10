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

func setupTestPublicationNWS005Router(pubHandler *PublicationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	}

	pubs := r.Group("/api/v1/publications", authMiddleware)
	{
		pubs.POST("/:id/reactions", pubHandler.AddReaction)
		pubs.DELETE("/:id/reactions/:reaction", pubHandler.RemoveReaction)
		pubs.GET("/:id/reactions", pubHandler.ListReactions)
		pubs.POST("/:id/comments", pubHandler.AddComment)
		pubs.GET("/:id/comments", pubHandler.ListComments)
		pubs.GET("/:id/comments/:comment_id/replies", pubHandler.ListReplies)
		pubs.PUT("/:id/comments/:comment_id/pin", pubHandler.PinComment)
		pubs.DELETE("/:id/comments/:comment_id/pin", pubHandler.UnpinComment)
		pubs.PUT("/:id/comments/:comment_id/moderate", pubHandler.ModerateComment)
		pubs.PUT("/:id/comments/lock", pubHandler.LockComments)
		pubs.POST("/:id/comments/:comment_id/reactions", pubHandler.AddCommentReaction)
		pubs.DELETE("/:id/comments/:comment_id/reactions/:reaction", pubHandler.RemoveCommentReaction)
	}

	return r
}

func TestNWS005_HandlerValidation(t *testing.T) {
	pubSvc := service.NewPublicationService(nil, nil, zap.NewNop())
	pubHandler := NewPublicationHandler(pubSvc, zap.NewNop())
	router := setupTestPublicationNWS005Router(pubHandler)
	pubID := uuid.New().String()
	commentID := uuid.New().String()

	t.Run("add reaction with invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/not-a-uuid/reactions", bytes.NewBufferString(`{"reaction":"👍"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("add reaction without reaction field returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+pubID+"/reactions", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("add comment with invalid body returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+pubID+"/comments", bytes.NewBufferString(`{invalid_json}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list replies with invalid comment ID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/publications/"+pubID+"/comments/invalid-comment-uuid/replies", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("pin comment with invalid comment ID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/"+pubID+"/comments/not-a-uuid/pin", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unpin comment with invalid pub ID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/publications/not-a-uuid/comments/"+commentID+"/pin", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("moderate comment with invalid json returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/"+pubID+"/comments/"+commentID+"/moderate", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("lock comments with invalid pub ID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/publications/not-a-uuid/comments/lock", bytes.NewBufferString(`{"allow_comments":false}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("add comment reaction without reaction body returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/publications/"+pubID+"/comments/"+commentID+"/reactions", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

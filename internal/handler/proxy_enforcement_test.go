package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

func TestProxyEmbeddingsAndRerankBlockedByEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	alemHandler := NewAlemHandler(nil, zap.NewNop())
	unifiedHandler := NewUnifiedAIHandler(nil, zap.NewNop())

	router := gin.New()
	router.POST("/ai/embeddings", alemHandler.CreateEmbeddings)
	router.POST("/ai/rerank", alemHandler.Rerank)
	router.POST("/unified-ai/embeddings", unifiedHandler.CreateEmbeddings)
	router.POST("/unified-ai/rerank", unifiedHandler.Rerank)

	for _, path := range []string{
		"/ai/embeddings",
		"/ai/rerank",
		"/unified-ai/embeddings",
		"/unified-ai/rerank",
	} {
		t.Run("blocks "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"model":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), string(apperrors.ErrCodeAIChatAccess))
		})
	}
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestFileDeletionRequiresAuthenticatedIdentityEvenWithoutMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewFileHandler(nil, zap.NewNop())
	for _, handle := range []gin.HandlerFunc{h.DeleteFile, h.DeletePublicFile} {
		for _, identity := range []int64{0, -1} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			if identity != 0 {
				c.Set("user_id", identity)
			}
			c.Request = httptest.NewRequest(http.MethodDelete, "/?object_name=image/42/private.png&user_id=42", nil)
			c.Request.Header.Set("X-User-ID", "42")
			handle(c)
			require.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("user_id", int64(42))
		c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
		handle(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
}

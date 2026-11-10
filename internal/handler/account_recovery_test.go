package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryStrictRequestAndNoCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler(nil, nil, nil, zap.NewNop())
	h.SetRecoveryService(&auth.RecoveryService{})
	r := gin.New()
	r.POST("/complete", h.CompletePasswordRecovery)
	for _, body := range []string{`{"token":"` + strings.Repeat("A", 43) + `","new_password":"Password123!","user_id":999}`, `{"token":"short","new_password":"Password123!"}`, `{} {}`, `{"token":"` + strings.Repeat("A", 43) + `","new_password":"` + strings.Repeat("x", 9000) + `"}`} {
		req := httptest.NewRequest(http.MethodPost, "/complete", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		require.Equal(t, 400, out.Code)
		require.Equal(t, "no-store", out.Header().Get("Cache-Control"))
		require.Equal(t, "no-referrer", out.Header().Get("Referrer-Policy"))
	}
}

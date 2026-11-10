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

func TestInvitationLegacyRegistrationIsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler(nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.POST("/register", h.Register)
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"organization_id":"arbitrary","team_id":"arbitrary"}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	require.Equal(t, 403, out.Code)
}
func TestInvitationRegistrationRejectsScopeOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler(nil, nil, nil, zap.NewNop())
	h.SetInvitationService(&auth.InvitationService{})
	r := gin.New()
	r.POST("/register", h.RegisterByInvitation)
	for _, extra := range []string{`"organization_id":"foreign"`, `"role":"admin"`, `"team_id":"foreign"`} {
		body := `{"token":"token","code":"123456","username":"person","password":"Password123!","first_name":"First","last_name":"Last","iin":"123456789012",` + extra + `}`
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		require.Equal(t, 400, out.Code)
		require.Equal(t, "no-store", out.Header().Get("Cache-Control"))
	}
}

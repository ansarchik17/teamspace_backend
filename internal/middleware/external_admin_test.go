package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestExternalAdminMiddlewareAllowsMatchingClientAndRole(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	mw := NewExternalAdminMiddleware(config.ExternalAdminConfig{
		Enabled:  true,
		ClientID: "bolek-admin",
		Roles:    []string{"external_user_admin"},
	}, zap.NewNop())

	rec := httptest.NewRecorder()
	_, r := gin.CreateTestContext(rec)

	called := false
	r.Use(func(c *gin.Context) {
		c.Set("azp", "bolek-admin")
		c.Set("roles", []string{"external_user_admin"})
		c.Next()
	})
	r.Use(mw.RequireAccess())
	r.GET("/api/v1/external-admin/users", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/external-admin/users", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("expected downstream handler to be called")
	}
}

func TestExternalAdminMiddlewareRejectsWrongClient(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	mw := NewExternalAdminMiddleware(config.ExternalAdminConfig{
		Enabled:  true,
		ClientID: "bolek-admin",
		Roles:    []string{"external_user_admin"},
	}, zap.NewNop())

	rec := httptest.NewRecorder()
	_, r := gin.CreateTestContext(rec)

	r.Use(func(c *gin.Context) {
		c.Set("azp", "alem-workspace")
		c.Set("roles", []string{"external_user_admin"})
		c.Next()
	})
	r.Use(mw.RequireAccess())
	r.GET("/api/v1/external-admin/users", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/external-admin/users", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

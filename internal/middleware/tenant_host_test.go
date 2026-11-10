package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type tenantResolverStub struct {
	slug string
	err  error
}

func (s tenantResolverStub) OrganizationSlugByUserID(context.Context, int64) (string, error) {
	return s.slug, s.err
}

func TestTenantSlugFromHost(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		slug     string
		isTenant bool
		valid    bool
	}{
		{name: "root", host: "teamspace.kz", valid: true},
		{name: "root with port", host: "teamspace.kz:443", valid: true},
		{name: "tenant", host: "company2.teamspace.kz", slug: "company2", isTenant: true, valid: true},
		{name: "nested tenant rejected", host: "dept.company2.teamspace.kz", isTenant: true, valid: false},
		{name: "unrelated host", host: "example.com", valid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, isTenant, valid := tenantSlugFromHost(tt.host, "teamspace.kz")
			if slug != tt.slug || isTenant != tt.isTenant || valid != tt.valid {
				t.Fatalf("tenantSlugFromHost(%q) = (%q, %t, %t), want (%q, %t, %t)", tt.host, slug, isTenant, valid, tt.slug, tt.isTenant, tt.valid)
			}
		})
	}
}

func TestTenantHostMiddlewareRejectsCrossTenantUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	middleware := NewTenantHostMiddleware("teamspace.kz", tenantResolverStub{slug: "other-company"}, nil)
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	})
	router.Use(middleware.Validate())
	router.GET("/", func(c *gin.Context) { c.Status(204) })

	req := httptest.NewRequest("GET", "https://company2.teamspace.kz/", nil)
	req.Host = "company2.teamspace.kz"
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403", res.Code)
	}
}

func TestTenantHostMiddlewareAllowsMatchingTenantUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	middleware := NewTenantHostMiddleware("teamspace.kz", tenantResolverStub{slug: "company2"}, nil)
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	})
	router.Use(middleware.Validate())
	router.GET("/", func(c *gin.Context) { c.Status(204) })

	req := httptest.NewRequest("GET", "https://company2.teamspace.kz/", nil)
	req.Host = "company2.teamspace.kz"
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204", res.Code)
	}
}

func TestTenantHostMiddlewareAllowsRootHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	middleware := NewTenantHostMiddleware("teamspace.kz", tenantResolverStub{err: errors.New("not used")}, nil)
	router.Use(middleware.Validate())
	router.GET("/", func(c *gin.Context) { c.Status(204) })

	req := httptest.NewRequest("GET", "https://teamspace.kz/", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204", res.Code)
	}
}

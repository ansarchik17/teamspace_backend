package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"go.uber.org/zap"
)

type loginProviderResolverStub struct {
	alias string
	err   error
	slug  string
	calls int
}

func (s *loginProviderResolverStub) ResolveEnabledAliasByOrganizationSlug(_ context.Context, slug string) (string, error) {
	s.slug = slug
	s.calls++
	return s.alias, s.err
}

func TestLoginOptionsCompanyDiscovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, slug, alias string
		err               error
		configured, want  bool
		calls             int
	}{
		{name: "connected", slug: " ACME ", alias: "private-broker", configured: true, want: true, calls: 1},
		{name: "not configured", slug: "acme"},
		{name: "disabled or missing", slug: "acme", err: errors.New("private diagnostic"), configured: true, calls: 1},
		{name: "database unavailable", slug: "acme", err: context.DeadlineExceeded, configured: true, calls: 1},
		{name: "empty alias", slug: "acme", alias: " ", configured: true, calls: 1},
		{name: "no company", configured: true},
		{name: "invalid slug", slug: "../acme", configured: true},
		{name: "email is not a slug", slug: "user@acme.example", configured: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(nil, nil, nil, zap.NewNop())
			resolver := &loginProviderResolverStub{alias: tt.alias, err: tt.err}
			if tt.configured {
				h.SetOrganizationIdentityProviderResolver(resolver)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/login-options?organization="+url.QueryEscape(tt.slug), nil)
			h.LoginOptions(c)
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(body) != 1 || body["corporate_login"] != tt.want {
				t.Fatalf("unexpected public response: %d %s", w.Code, w.Body)
			}
			if resolver.calls != tt.calls {
				t.Fatalf("resolver calls=%d want %d", resolver.calls, tt.calls)
			}
			if tt.calls > 0 && resolver.slug != "acme" {
				t.Fatalf("slug=%q", resolver.slug)
			}
			if w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
				t.Fatal("discovery must neither cache nor set auth cookies")
			}
		})
	}
}

func TestCorporateLoginRechecksProviderAndUsesBroker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name     string
		resolver *loginProviderResolverStub
		status   int
	}{
		{"connected", &loginProviderResolverStub{alias: "workspace-acme"}, 302},
		{"disabled since discovery", &loginProviderResolverStub{err: errors.New("disabled")}, 404},
		{"resolver unavailable", nil, 404},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := auth.NewKeycloakService(&authHandlerUserRepoStub{}, nil, nil, keycloak.NewWithPublicURL("http://keycloak:8080", "https://login.example.com", "test", "workspace", "test-secret"), nil, "test")
			h := NewAuthHandler(svc, nil, nil, zap.NewNop())
			if tt.resolver != nil {
				h.SetOrganizationIdentityProviderResolver(tt.resolver)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/keycloak/login?organization=acme&login_hint=user%40acme.example", nil)
			h.StartKeycloakLogin(c)
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if tt.status == 302 {
				target, err := url.Parse(w.Header().Get("Location"))
				if err != nil {
					t.Fatal(err)
				}
				if target.Query().Get("kc_idp_hint") != "workspace-acme" || target.Query().Get("login_hint") != "user@acme.example" {
					t.Fatalf("incorrect broker redirect %s", target)
				}
			} else if len(w.Result().Cookies()) != 0 {
				t.Fatal("unavailable provider started a session")
			}
		})
	}
}

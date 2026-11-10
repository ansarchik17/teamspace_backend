package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const meetTestSecret = "test-secret-with-more-than-32-characters"

func meetTestRouter(cfg config.QMeetConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", int64(42)) })
	router.Any("/api/v1/meet/*path", newMeetProxy(cfg, func(_ context.Context, id int64) (*domain.UserResponse, error) {
		return &domain.UserResponse{ID: id, Email: "person@example.test", FullName: "Person", IsActive: true}, nil
	}))
	return router
}

func TestMeetProxySignsServerIdentityAndStripsCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/app/v1/calendar", r.URL.Path)
		require.Equal(t, "start=2026-10-04", r.URL.RawQuery)
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		token, err := jwt.Parse(r.Header.Get("X-Workspace-Assertion"), func(_ *jwt.Token) (any, error) { return []byte(meetTestSecret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("qmeet-workspace"), jwt.WithIssuer("test-workspace"))
		require.NoError(t, err)
		claims := token.Claims.(jwt.MapClaims)
		require.Equal(t, "42", claims["sub"])
		require.Equal(t, "person@example.test", claims["email"])
		require.LessOrEqual(t, claims["exp"].(float64)-claims["iat"].(float64), float64(30))
		w.Header().Set("Set-Cookie", "qmeet_session=do-not-leak")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()
	router := meetTestRouter(config.QMeetConfig{BaseURL: upstream.URL, Secret: meetTestSecret, Issuer: "test-workspace"})
	req := httptest.NewRequest("GET", "/api/v1/meet/calendar?start=2026-10-04", nil)
	req.Header.Set("Authorization", "Bearer workspace-secret")
	req.Header.Set("Cookie", "qmeet_session=someone-else")
	req.Header.Set("X-Workspace-Assertion", "attacker-controlled")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, req)
	require.Equal(t, 200, result.Code)
	require.Empty(t, result.Header().Get("Set-Cookie"))
	require.Equal(t, "no-store", result.Header().Get("Cache-Control"))
	require.Equal(t, `[]`, result.Body.String())
}

func TestMeetProxyDisabledAndRejectsUnrelatedAPIs(t *testing.T) {
	router := meetTestRouter(config.QMeetConfig{})
	for _, path := range []string{"/internal/agent/v1/meetings", "/auth/login", "/calls/a/../../auth/login", "/calls/a/recording/start"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/v1/meet"+path, strings.NewReader(`{}`)))
		require.Equal(t, 404, recorder.Code, path)
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest("GET", "/api/v1/meet/auth/me", nil))
	require.Equal(t, 503, result.Code)
	require.Contains(t, result.Body.String(), "meet_not_configured")
}

func TestMeetProxyUpstreamIdentityErrorsDoNotExpireWorkspace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"detail":"Invalid credentials"}`))
	}))
	defer upstream.Close()
	router := meetTestRouter(config.QMeetConfig{BaseURL: upstream.URL, Secret: meetTestSecret, Issuer: "test-workspace"})
	for _, test := range []struct {
		method, path string
		status       int
	}{{"GET", "/auth/me", 502}, {"POST", "/auth/workspace/link", 422}} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(test.method, "/api/v1/meet"+test.path, strings.NewReader(`{}`)))
		require.Equal(t, test.status, recorder.Code)
	}
}

func TestMeetProxyPreservesLinkRequiredAndLimitsRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"detail":{"code":"meet_account_link_required","message":"Confirm QMeet password"}}`))
	}))
	defer upstream.Close()
	router := meetTestRouter(config.QMeetConfig{BaseURL: upstream.URL, Secret: meetTestSecret, Issuer: "test-workspace"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/meet/auth/me", nil))
	require.Equal(t, 409, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"meet_account_link_required"`)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/v1/meet/calls", strings.NewReader(strings.Repeat("x", 65537))))
	require.Equal(t, 413, recorder.Code)
}

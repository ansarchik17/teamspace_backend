package middleware

import (
	"context"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type securityRecorderTest struct{ events []domain.SecurityAuditEvent }

func (r *securityRecorderTest) RecordSecurityEvent(_ context.Context, e domain.SecurityAuditEvent) error {
	r.events = append(r.events, e)
	return nil
}
func TestSecurityClientIPTrustedProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	require.Equal(t, "192.0.2.1", SecurityClientIP("192.0.2.1:1234", "198.51.100.9", trusted))
	require.Equal(t, "198.51.100.9", SecurityClientIP("10.0.0.2:1234", "192.0.2.44,198.51.100.9", trusted))
	require.Equal(t, "192.0.2.44", SecurityClientIP("10.0.0.2:1234", "192.0.2.44,10.0.0.3", trusted))
	require.Equal(t, "10.0.0.2", SecurityClientIP("10.0.0.2:1234", "forged", trusted))
	require.Equal(t, "2001:db8::1", SecurityClientIP("[2001:db8::1]:1234", "198.51.100.9", nil))
}
func TestSecurityAuditHTTPClassificationAndPrivacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		method, path, route, action string
		status                      int
		actor                       int64
	}{
		{"POST", "/api/v1/auth/login", "/api/v1/auth/login", "auth.login", 401, 0},
		{"GET", "/api/v1/auth/keycloak/callback?code=private-code", "/api/v1/auth/keycloak/callback", "auth.login", 302, 0},
		{"GET", "/api/v1/auth/keycloak/callback?code=private-code", "/api/v1/auth/keycloak/callback", "auth.login", 302, 3},
		{"POST", "/api/v1/drive/files", "/api/v1/drive/files", "file.files.create", 201, 3},
		{"DELETE", "/api/v1/drive/share/link/private-token", "/api/v1/drive/share/link/:token", "file.share.link.delete", 204, 3},
		{"PUT", "/api/v1/kanban/tasks/task-id/status", "/api/v1/kanban/tasks/:id/status", "task.tasks.status.update", 200, 3},
		{"GET", "/api/v1/kanban/tasks/task-id", "/api/v1/kanban/tasks/:id", "access.denied", 403, 3},
		{"GET", "/api/v1/drive/items", "/api/v1/drive/items", "", 200, 3},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			rec := &securityRecorderTest{}
			r := gin.New()
			r.Use(RequestID(), SecurityAudit(rec, zap.NewNop(), nil))
			r.Handle(tc.method, tc.route, func(c *gin.Context) { SetSecurityActor(c, tc.actor); c.Status(tc.status) })
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"password":"private-password"}`))
			req.RemoteAddr = "192.0.2.55:444"
			req.Header.Set("Authorization", "Bearer private-bearer")
			req.Header.Set("X-Forwarded-For", "198.51.100.1")
			req.Header.Set("X-Request-ID", "=private-formula")
			r.ServeHTTP(httptest.NewRecorder(), req)
			if tc.action == "" {
				require.Empty(t, rec.events)
				return
			}
			require.Len(t, rec.events, 1)
			e := rec.events[0]
			require.Equal(t, tc.action, e.Action)
			require.Equal(t, tc.route, e.Route)
			require.Equal(t, "192.0.2.55", *e.IP)
			require.NotContains(t, e.RequestID, "private")
			require.NotContains(t, e.Route, "private")
			if tc.status >= 400 || tc.action == "auth.login" && tc.status >= 300 && tc.actor == 0 {
				require.Equal(t, "failure", e.Outcome)
			} else {
				require.Equal(t, "success", e.Outcome)
			}
		})
	}
}

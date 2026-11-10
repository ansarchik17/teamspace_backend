package middleware

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type SecurityAuditRecorder interface {
	RecordSecurityEvent(context.Context, domain.SecurityAuditEvent) error
}

// Forwarded IPs are honored only when the immediate peer belongs to an
// explicitly configured proxy CIDR. Without configuration the socket peer wins.
func SecurityClientIP(requestPeer, forwarded string, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(requestPeer)
	if err != nil {
		host = requestPeer
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	peer = peer.Unmap()
	isTrusted := func(ip netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !isTrusted(peer) {
		return peer.String()
	}
	chain := strings.Split(forwarded, ",")
	if len(chain) > 32 {
		return peer.String()
	}
	current := peer
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrusted(current) {
			break
		}
		candidate, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return peer.String()
		}
		current = candidate.Unmap()
	}
	return current.String()
}
func SecurityAction(method, route string) string {
	switch route {
	case "/api/v1/auth/login", "/api/v1/auth/keycloak/callback", "/auth/login", "/api/v1/auth/keycloak/mobile/callback", "/api/v1/auth/pin/login-by-pin", "/api/v1/auth/egov/callback", "/api/v1/auth/keycloak/exchange", "/api/v1/auth/keycloak/bootstrap", "/api/v1/auth/tenant-handoff/redeem", "/api/v1/auth/keycloak/sso-check/callback", "/api/v1/auth/keycloak/silent/callback", "/api/v1/auth/external-admin/keycloak/callback", "/api/v1/external-admin/auth/keycloak/callback":
		return "auth.login"
	case "/api/v1/auth/logout", "/api/v1/auth/logout-all", "/api/v1/auth/keycloak/logout", "/api/v1/external-admin/auth/keycloak/logout":
		return "auth.logout"
	case "/api/v1/auth/password":
		return "auth.password.change"
	}
	if strings.HasSuffix(route, "/security-audit/export") {
		return "audit.export"
	}
	mutation := method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
	if !mutation {
		if method == "GET" && (strings.HasPrefix(route, "/api/v1/drive/") || strings.HasPrefix(route, "/api/v1/public/drive/") || route == "/api/v1/files/download") && strings.HasSuffix(route, "/download") {
			return "file.download"
		}
		return ""
	}
	category := ""
	switch {
	case strings.HasPrefix(route, "/api/v1/drive/"):
		if strings.Contains(route, "/search") || strings.Contains(route, "/favorites") {
			return ""
		}
		category = "file"
	case strings.HasPrefix(route, "/api/v1/files"):
		category = "file"
	case strings.HasPrefix(route, "/api/v1/kanban/"):
		category = "task"
	case strings.HasPrefix(route, "/api/v1/organizations") || strings.HasPrefix(route, "/api/v1/platform/organizations") || strings.HasPrefix(route, "/api/v1/external-admin/"):
		return "admin.request"
	default:
		return ""
	}
	prefix := "/api/v1/drive/"
	if category == "task" {
		prefix = "/api/v1/kanban/"
	}
	if strings.HasPrefix(route, "/api/v1/files") {
		prefix = "/api/v1/"
	}
	parts := []string{category}
	for _, p := range strings.Split(strings.TrimPrefix(route, prefix), "/") {
		if p != "" && !strings.HasPrefix(p, ":") {
			parts = append(parts, strings.ReplaceAll(p, "-", "_"))
		}
	}
	verb := map[string]string{"POST": "create", "PUT": "update", "PATCH": "update", "DELETE": "delete"}[method]
	return strings.Join(append(parts, verb), ".")
}
func SetSecurityActor(c *gin.Context, id int64) {
	if id > 0 {
		c.Set("security_actor_id", id)
	}
}
func SecurityAudit(recorder SecurityAuditRecorder, logger *zap.Logger, trusted []netip.Prefix) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetString("request_id")
		if len(requestID) > 128 || strings.IndexFunc(requestID, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.", r))
		}) >= 0 {
			requestID = uuid.NewString()
		}
		meta := &domain.SecurityRequestMetadata{IP: SecurityClientIP(c.Request.RemoteAddr, c.GetHeader("X-Forwarded-For"), trusted), RequestID: requestID}
		c.Request = c.Request.WithContext(domain.WithSecurityMetadata(c.Request.Context(), meta))
		c.Next()
		route := c.FullPath()
		if route == "" || len(route) > 255 {
			return
		}
		status := c.Writer.Status()
		action := SecurityAction(c.Request.Method, route)
		if action != "auth.login" && (status == 401 || status == 403) {
			action = "access.denied"
		}
		if action == "" {
			return
		}
		outcome := "success"
		if status >= 400 {
			outcome = "failure"
		}
		if action == "auth.login" && status >= 300 && c.GetInt64("security_actor_id") == 0 {
			outcome = "failure"
		}
		if action == "admin.request" && outcome == "success" && meta.AdministrationRecorded {
			return
		}
		event := domain.SecurityAuditEvent{Action: action, Outcome: outcome, RequestID: requestID, Method: c.Request.Method, Route: route, Status: status}

		for _, key := range []string{"id", "fileId", "unit_id", "user_id"} {
			raw := c.Param(key)
			if parsed, err := uuid.Parse(raw); err == nil {
				event.ObjectID = parsed.String()
				if strings.Contains(route, "/kanban/tasks/") {
					event.ObjectType = "task"
				} else if strings.Contains(route, "/kanban/boards/") {
					event.ObjectType = "board"
				} else if strings.Contains(route, "/drive/folders/") {
					event.ObjectType = "folder"
				} else if strings.Contains(route, "/drive/files/") {
					event.ObjectType = "file"
				}
				break
			}
		}
		id := c.GetInt64("security_actor_id")
		if id <= 0 {
			if actor, ok := GetUserID(c); ok {
				id = actor
			}
		}
		if id > 0 {
			event.ActorID = &id
		}
		if meta.IP != "" {
			event.IP = &meta.IP
		}
		// Only a successfully authorized platform request may select a company.
		if outcome == "success" && strings.HasPrefix(route, "/api/v1/platform/organizations/") {
			if org, err := uuid.Parse(c.Param("organization_id")); err == nil {
				event.OrganizationID = &org
			}
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
		defer cancel()
		if err := recorder.RecordSecurityEvent(ctx, event); err != nil {
			logger.Error("security audit persistence failed", zap.String("action", action), zap.String("request_id", requestID), zap.Error(err))
		}
	}
}

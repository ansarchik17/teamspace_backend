package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

// TenantHostResolver resolves the organization assigned to an authenticated user.
type TenantHostResolver interface {
	OrganizationSlugByUserID(ctx context.Context, userID int64) (string, error)
}

// TenantHostMiddleware binds a tenant subdomain to the authenticated user's
// organization. The root host remains the platform host; only one-label hosts
// such as company2.teamspace.kz are treated as tenant hosts.
type TenantHostMiddleware struct {
	baseDomain string
	resolver   TenantHostResolver
	logger     *zap.Logger
}

func NewTenantHostMiddleware(baseDomain string, resolver TenantHostResolver, logger *zap.Logger) *TenantHostMiddleware {
	return &TenantHostMiddleware{
		baseDomain: normalizeBaseDomain(baseDomain),
		resolver:   resolver,
		logger:     logger,
	}
}

func (m *TenantHostMiddleware) BaseDomain() string { return m.baseDomain }

func (m *TenantHostMiddleware) OrganizationSlug(ctx context.Context, userID int64) (string, error) {
	if m.resolver == nil {
		return "", apperrors.Forbidden("tenant resolver is unavailable")
	}
	return m.resolver.OrganizationSlugByUserID(ctx, userID)
}

func (m *TenantHostMiddleware) Validate() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := GetUserID(c)
		if !m.ValidateUser(c, userID) {
			return
		}
		c.Next()
	}
}

// ValidateUser also guards public authentication endpoints before newly issued
// tokens are returned. Protected endpoints call it after authentication.
func (m *TenantHostMiddleware) ValidateUser(c *gin.Context, userID int64) bool {
	tenantSlug, isTenant, valid := tenantSlugFromHost(c.Request.Host, m.baseDomain)
	if !valid {
		AbortJSON(c, http.StatusBadRequest, apperrors.Validation("invalid tenant host"))
		return false
	}
	if !isTenant {
		return true
	}
	if userID <= 0 || m.resolver == nil {
		AbortJSON(c, http.StatusForbidden, apperrors.Forbidden("tenant host requires an organization user"))
		return false
	}
	organizationSlug, err := m.resolver.OrganizationSlugByUserID(c.Request.Context(), userID)
	if err != nil || organizationSlug == "" || !strings.EqualFold(tenantSlug, organizationSlug) {
		if m.logger != nil {
			m.logger.Warn("tenant host does not match authenticated organization",
				zap.Int64("user_id", userID),
				zap.String("tenant_slug", tenantSlug),
				zap.String("organization_slug", organizationSlug),
				zap.Error(err),
			)
		}
		AbortJSON(c, http.StatusForbidden, apperrors.Forbidden("tenant host does not match user organization"))
		return false
	}
	c.Set("tenant_slug", tenantSlug)
	return true
}

func normalizeBaseDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	return value
}

func tenantSlugFromHost(rawHost, baseDomain string) (string, bool, bool) {
	host := strings.ToLower(strings.TrimSpace(rawHost))
	if host == "" {
		return "", false, false
	}
	if colon := strings.LastIndex(host, ":"); colon > -1 {
		host = host[:colon]
	}
	if host == baseDomain || host == "www."+baseDomain || baseDomain == "" {
		return "", false, true
	}
	suffix := "." + baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false, true
	}
	slug := strings.TrimSuffix(host, suffix)
	if slug == "" || strings.Contains(slug, ".") || strings.ContainsAny(slug, " /_:") {
		return "", true, false
	}
	return slug, true, true
}

// TenantSlugFromHost exposes the host parsing rule to auth redirect builders
// so OAuth callbacks stay on the tenant host instead of falling back to the
// platform host.
func TenantSlugFromHost(rawHost, baseDomain string) (string, bool, bool) {
	return tenantSlugFromHost(rawHost, normalizeBaseDomain(baseDomain))
}

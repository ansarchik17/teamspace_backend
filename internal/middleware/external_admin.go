package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

type ExternalAdminMiddleware struct {
	cfg    config.ExternalAdminConfig
	logger *zap.Logger
}

func NewExternalAdminMiddleware(cfg config.ExternalAdminConfig, logger *zap.Logger) *ExternalAdminMiddleware {
	return &ExternalAdminMiddleware{
		cfg:    cfg,
		logger: logger,
	}
}

func (m *ExternalAdminMiddleware) RequireAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.cfg.Enabled {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		azp, _ := c.Get("azp")
		authorizedParty, _ := azp.(string)
		if strings.TrimSpace(authorizedParty) == "" {
			m.respondError(c, apperrors.Forbidden("external admin endpoints require a Keycloak access token"))
			c.Abort()
			return
		}
		if authorizedParty != m.cfg.ClientID {
			m.logger.Warn("external admin client mismatch",
				zap.String("expected_client_id", m.cfg.ClientID),
				zap.String("actual_client_id", authorizedParty),
				zap.String("path", c.Request.URL.Path),
			)
			m.respondError(c, apperrors.Forbidden("client is not allowed to access external admin endpoints"))
			c.Abort()
			return
		}

		rawRoles, exists := c.Get("roles")
		if !exists {
			m.respondError(c, apperrors.Forbidden("missing required external admin role"))
			c.Abort()
			return
		}
		roles, _ := rawRoles.([]string)
		if !hasAnyRole(roles, m.cfg.Roles) {
			m.respondError(c, apperrors.Forbidden("missing required external admin role"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func hasAnyRole(userRoles, allowedRoles []string) bool {
	for _, userRole := range userRoles {
		userRole = strings.TrimSpace(userRole)
		if userRole == "" {
			continue
		}
		for _, allowedRole := range allowedRoles {
			if userRole == strings.TrimSpace(allowedRole) {
				return true
			}
		}
	}
	return false
}

func (m *ExternalAdminMiddleware) respondError(c *gin.Context, err *apperrors.AppError) {
	RespondAppError(c, err)
}

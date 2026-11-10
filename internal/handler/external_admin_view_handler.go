package handler

import (
	"embed"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

//go:embed assets/external_admin_panel.html
var externalAdminViewAssets embed.FS

type ExternalAdminViewHandler struct {
	logger *zap.Logger
}

func NewExternalAdminViewHandler(logger *zap.Logger) *ExternalAdminViewHandler {
	return &ExternalAdminViewHandler{
		logger: logger.With(zap.String("handler", "external_admin_view")),
	}
}

func (h *ExternalAdminViewHandler) Serve(c *gin.Context) {
	content, err := externalAdminViewAssets.ReadFile("assets/external_admin_panel.html")
	if err != nil {
		if h != nil && h.logger != nil {
			h.logger.Error("Failed to load external admin panel asset", zap.Error(err))
		}
		respondAppError(c, apperrors.Internal("failed to load external admin panel"))
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:;")
	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
}

func (h *ExternalAdminViewHandler) ServeIfEnabled(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled {
			c.Status(http.StatusNotFound)
			return
		}

		// Strip trailing slash redirects at the browser level and always return the app shell.
		if strings.HasPrefix(c.Request.URL.Path, "/external-admin") {
			h.Serve(c)
			return
		}

		c.Status(http.StatusNotFound)
	}
}

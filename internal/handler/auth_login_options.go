package handler

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var loginOrganizationSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// LoginOptions exposes only whether a company offers corporate login. It never
// looks up a person, returns provider configuration, or starts an SSO session.
func (h *AuthHandler) LoginOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	available := false
	slug := strings.ToLower(strings.TrimSpace(c.Query("organization")))
	if loginOrganizationSlug.MatchString(slug) && h.organizationIdentityProviderResolver != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		alias, err := h.organizationIdentityProviderResolver.ResolveEnabledAliasByOrganizationSlug(ctx, slug)
		available = err == nil && strings.TrimSpace(alias) != ""
	}
	// Missing, disabled, suspended and unavailable configurations all fail closed.
	respondJSON(c, http.StatusOK, gin.H{"corporate_login": available})
}

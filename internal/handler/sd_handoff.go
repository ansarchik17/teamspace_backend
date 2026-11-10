package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// A verified Workspace session works for both password and Keycloak login.
// The shared cookie carries a random browser binding, never session tokens.
func (h *OrganizationHandler) CreateSDHandoff(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	base, err := url.Parse(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")))
	if err != nil || base.Host == "" || base.Scheme != "https" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		h.respondError(c, apperrors.Conflict("Публичный адрес Workspace для входа в SenimDesk настроен некорректно"))
		return
	}
	value := make([]byte, 32)
	if _, err = rand.Read(value); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	browser := base64.RawURLEncoding.EncodeToString(value)
	hash := sha256.Sum256([]byte(browser))
	target, err := h.service.CreateSDHandoff(c.Request.Context(), middleware.MustGetUserID(c), hex.EncodeToString(hash[:]))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	redirect, parseErr := url.Parse(target)
	if parseErr != nil || redirect.Scheme != base.Scheme || redirect.User != nil ||
		!strings.HasSuffix(redirect.Hostname(), "."+base.Hostname()) || redirect.Port() != base.Port() ||
		redirect.Path != "/sd/auth/callback" || redirect.RawQuery != "" {
		h.respondError(c, apperrors.Conflict("SenimDesk вернул некорректный адрес входа"))
		return
	}
	fragment, err := url.ParseQuery(redirect.Fragment)
	if err != nil || len(fragment.Get("handoff")) != 43 {
		h.respondError(c, apperrors.Conflict("SenimDesk не вернул код входа"))
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "__Secure-sd-sso-browser", Value: browser, Domain: base.Hostname(), Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	respondJSON(c, http.StatusOK, gin.H{"redirect_url": target})
}

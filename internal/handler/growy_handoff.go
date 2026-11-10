package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

const growyHandoffTTL = 45

func (h *OrganizationHandler) CreateGrowyHandoff(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok || claims.UserID <= 0 || strings.TrimSpace(claims.Email) == "" {
		respondJSON(c, http.StatusUnauthorized, gin.H{"code": "workspace_identity_unavailable"})
		return
	}
	details, err := h.service.GrowyHandoffDetails(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrOrganizationNotFound) {
			respondJSON(c, http.StatusForbidden, gin.H{"code": "growy_company_access_not_configured"})
			return
		}
		respondJSON(c, http.StatusInternalServerError, gin.H{"code": "growy_sso_failed"})
		return
	}
	lmsBase, err := service.GrowyLMSPublicBase()
	if err != nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"code": "growy_sso_not_configured"})
		return
	}
	if err := h.service.ProvisionGrowyLMSTenant(c.Request.Context(), details.OrganizationID, details.OrganizationName, details.OrganizationSlug, lmsBase); err != nil {
		h.logger.Error("Could not provision Growy tenant before SSO handoff", zap.Error(err))
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"code": "growy_company_provisioning_failed"})
		return
	}
	ticket, err := service.SignGrowyUserTicket(claims.UserID, claims.Email, claims.Username, claims.FirstName, claims.LastName, details)
	if err != nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"code": "growy_sso_not_configured"})
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	respondJSON(c, http.StatusOK, gin.H{"ticket": ticket, "expires_in": growyHandoffTTL})
}

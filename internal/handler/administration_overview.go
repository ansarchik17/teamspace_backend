package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func (h *OrganizationHandler) AdministrationOverview(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.AdministrationOverview(c.Request.Context(), middleware.MustGetUserID(c), org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) PlatformAdministrationOverview(c *gin.Context) {
	out, err := h.service.PlatformAdministrationOverview(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}

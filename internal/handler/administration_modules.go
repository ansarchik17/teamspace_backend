package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
)

func (h *OrganizationHandler) ListAdministrationModules(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.ListAdministrationModules(c.Request.Context(), middleware.MustGetUserID(c), org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) SetAdministrationModule(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var req domain.AdministrationModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid module request"))
		return
	}
	if err := h.service.SetAdministrationModule(c.Request.Context(), middleware.MustGetUserID(c), org, c.Param("application"), req); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *OrganizationHandler) RequireModule(app string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := h.service.RequireUserModule(c.Request.Context(), middleware.MustGetUserID(c), app); err != nil {
			h.respondError(c, organizationAppError(err))
			c.Abort()
			return
		}
		c.Next()
	}
}

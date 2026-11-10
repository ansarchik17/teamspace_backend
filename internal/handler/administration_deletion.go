package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"strconv"
)

func (h *OrganizationHandler) DeleteAdministrationUser(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || id <= 0 {
		h.respondError(c, apperrors.Validation("invalid user id"))
		return
	}
	if err = h.service.DeleteAdministrationUser(c.Request.Context(), middleware.MustGetUserID(c), id, org); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *OrganizationHandler) DeletePlatformOrganization(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil || org == nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	if err = h.service.DeletePlatformOrganization(c.Request.Context(), middleware.MustGetUserID(c), *org); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

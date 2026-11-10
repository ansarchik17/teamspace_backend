package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"strconv"
)

func (h *OrganizationHandler) AdministrationStorage(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.AdministrationStorage(c.Request.Context(), middleware.MustGetUserID(c), org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) AdministrationStorageUsers(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "25"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid storage pagination"))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid storage pagination"))
		return
	}
	out, err := h.service.AdministrationStorageUsers(c.Request.Context(), middleware.MustGetUserID(c), org, limit, offset)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) SetAdministrationStorage(c *gin.Context) {
	h.changeAdministrationStorage(c, false)
}
func (h *OrganizationHandler) SetAdministrationUserStorage(c *gin.Context) {
	h.changeAdministrationStorage(c, true)
}
func (h *OrganizationHandler) changeAdministrationStorage(c *gin.Context, user bool) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var req domain.AdministrationStorageRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.Valid() {
		h.respondError(c, apperrors.Validation("invalid storage quota"))
		return
	}
	actor := middleware.MustGetUserID(c)
	if user {
		id, parseErr := strconv.ParseInt(c.Param("user_id"), 10, 64)
		if parseErr != nil || id <= 0 {
			h.respondError(c, apperrors.Validation("invalid user id"))
			return
		}
		err = h.service.SetAdministrationUserStorage(c.Request.Context(), actor, id, org, req)
	} else {
		err = h.service.SetAdministrationStorage(c.Request.Context(), actor, org, req)
	}
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *OrganizationHandler) AdministrationStorageCapacity(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil || org == nil {
		h.respondError(c, apperrors.Validation("organization required"))
		return
	}
	out, err := h.service.AdministrationStorageCapacity(c.Request.Context(), middleware.MustGetUserID(c), *org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}

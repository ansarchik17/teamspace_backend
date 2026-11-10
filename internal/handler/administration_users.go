package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"strconv"
)

func (h *OrganizationHandler) ListAdministrationUsers(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "25"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid user pagination"))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid user pagination"))
		return
	}
	out, err := h.service.ListAdministrationUsers(c.Request.Context(), middleware.MustGetUserID(c), org, domain.AdministrationUserFilter{Query: c.Query("q"), Limit: limit, Offset: offset})
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) SetEmployeeBlocked(c *gin.Context) {
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var req domain.AdministrationUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Blocked == nil {
		h.respondError(c, apperrors.Validation("blocked must be a boolean"))
		return
	}
	if err := h.service.SetEmployeeBlocked(c.Request.Context(), middleware.MustGetUserID(c), id, org, *req.Blocked); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

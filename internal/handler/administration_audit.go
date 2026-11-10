package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"strconv"
)

func (h *OrganizationHandler) ListAdministrationAudit(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid audit pagination"))
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid audit pagination"))
		return
	}
	var org *uuid.UUID
	platform := c.FullPath() == "/api/v1/platform/administration/audit" || c.Param("organization_id") != ""
	if raw := c.Param("organization_id"); raw != "" {
		id, e := uuid.Parse(raw)
		if e != nil {
			h.respondError(c, apperrors.Validation("invalid organization"))
			return
		}
		org = &id
	}
	out, err := h.service.ListAdministrationAudit(c.Request.Context(), middleware.MustGetUserID(c), org, platform, domain.AdministrationAuditFilter{Limit: limit, BeforeID: before})
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}

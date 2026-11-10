package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func (h *OrganizationHandler) SDOpenTicketCount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.Request.URL.RawQuery != "" {
		h.respondError(c, apperrors.Validation("query parameters are not supported"))
		return
	}
	count, err := h.service.SDOpenTicketCount(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"count": count})
}

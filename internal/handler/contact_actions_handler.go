package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// ContactActions returns fresh shortcuts for a company employee.
// @Summary Get employee quick actions
// @Description Read-only navigation data: chat peer, safe mailto URL and meeting composer defaults. Also included in the employee profile.
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "Employee ID"
// @Success 200 {object} domain.ContactActions
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Router /contacts/{user_id}/actions [get]
func (h *OrganizationHandler) ContactActions(c *gin.Context) {
	id, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	result, err := h.service.ContactActions(c.Request.Context(), middleware.MustGetUserID(c), id)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

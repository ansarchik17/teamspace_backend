package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// OrganizationTree returns the active organization structure as a nested tree.
// @Summary Get company organization tree
// @Tags organizations
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.OrganizationTree
// @Failure 404 {object} apperrors.ErrorResponse
// @Failure 409 {object} apperrors.ErrorResponse
// @Router /organizations/current/tree [get]
func (h *OrganizationHandler) OrganizationTree(c *gin.Context) {
	result, err := h.service.OrganizationTree(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

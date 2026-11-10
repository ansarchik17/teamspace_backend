package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// CheckGrowyAccess is the remote CRM's internal callback. The LMS uses its own
// authentication; CRM token revocation must follow crm, not the learning module.
func (h *OrganizationHandler) CheckGrowyAccess(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input struct {
		UserID         int64     `json:"user_id" binding:"required,gt=0"`
		OrganizationID uuid.UUID `json:"organization_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"code": "invalid_access_check"})
		return
	}
	details, err := h.service.GrowyCRMHandoffDetails(c.Request.Context(), input.UserID)
	if errors.Is(err, domain.ErrOrganizationNotFound) || err == nil && details.OrganizationID != input.OrganizationID {
		respondJSON(c, http.StatusOK, gin.H{"active": false})
		return
	}
	if err != nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"code": "access_check_unavailable"})
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, gin.H{"active": true, "growy_role": details.GrowyRole, "growy_roles": details.AssignedGrowyRoles()})
}

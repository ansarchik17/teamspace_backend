package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"io"
	"net/http"
)

// OffboardContact transfers work and deactivates an employee atomically.
// @Summary Offboard employee and hand work to a successor
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param user_id path int true "Employee ID"
// @Param dry_run query bool false "Preview and roll back (default true)" default(true)
// @Param request body domain.ContactOffboardingRequest true "Successor and reason"
// @Success 200 {object} domain.ContactOffboardingResult
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Failure 409 {object} apperrors.ErrorResponse
// @Failure 507 {object} apperrors.ErrorResponse
// @Router /contacts/{user_id}/offboarding [post]
func (h *OrganizationHandler) OffboardContact(c *gin.Context) {
	target, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	dryRun := true
	if value, ok := c.GetQuery("dry_run"); ok {
		switch value {
		case "true":
		case "false":
			dryRun = false
		default:
			h.respondError(c, apperrors.Validation("dry_run must be true or false"))
			return
		}
	}
	var req domain.ContactOffboardingRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid offboarding request"))
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		h.respondError(c, apperrors.Validation("invalid offboarding request"))
		return
	}
	result, err := h.service.OffboardContact(c.Request.Context(), middleware.MustGetUserID(c), target, req, dryRun)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// ContactOffboarding returns the immutable handover record plus current identity synchronization status.
// @Summary Read employee offboarding result
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "Employee ID"
// @Success 200 {object} domain.ContactOffboardingResult
// @Failure 403 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Router /contacts/{user_id}/offboarding [get]
func (h *OrganizationHandler) ContactOffboarding(c *gin.Context) {
	target, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	result, err := h.service.ContactOffboarding(c.Request.Context(), middleware.MustGetUserID(c), target)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

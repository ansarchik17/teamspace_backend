package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"net/http"
)

// OfficeSaveState godoc
// @Summary Get last durable document checkpoint and save failure
// @Description Saved time refers to the Drive revision, not unsaved edits or DocumentServer connection health. Failures belong to the current revision and session only.
// @Tags docs
// @Security BearerAuth
// @Produce json
// @Param id path string true "Drive file UUID"
// @Success 200 {object} domain.OfficeSaveState
// @Failure 400,401,403,404 {object} map[string]interface{}
// @Router /docs/files/{id}/save-state [get]
func (h *DriveHandler) OfficeSaveState(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	state, err := h.drive.OfficeSaveState(c.Request.Context(), h.getUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, state)
}

// RecoverOfficeSession godoc
// @Summary Recover a document from its latest saved Drive snapshot
// @Description Explicitly abandons unsaved changes in the old editor session. Appends a new revision and rotates the session key; stale callbacks cannot overwrite recovery. Requires editor access and expected_version. Reopen using editor-link afterwards.
// @Tags docs
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Drive file UUID"
// @Param body body domain.OfficeRecoveryRequest true "Expected current revision"
// @Success 200 {object} map[string]int
// @Failure 400,401,403,404,409,415 {object} map[string]interface{}
// @Router /docs/files/{id}/recover [post]
func (h *DriveHandler) RecoverOfficeSession(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	var req domain.OfficeRecoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("expected_version is required"))
		return
	}
	next, err := h.drive.RecoverOfficeSession(c.Request.Context(), h.getUserID(c), id, req.ExpectedVersion)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"version": next})
}

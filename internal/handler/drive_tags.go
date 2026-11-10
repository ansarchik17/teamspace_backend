package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"net/http"
)

// GetFileTags godoc
// @Summary Get file tags
// @Tags drive
// @Security BearerAuth
// @Param id path string true "File UUID"
// @Success 200 {object} map[string]interface{}
// @Router /drive/files/{id}/tags [get]
func (h *DriveHandler) GetFileTags(c *gin.Context) {
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	tags, err := h.drive.GetFileTags(c.Request.Context(), h.getUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"tags": tags})
}

type driveTagsRequest struct {
	Tags *[]string `json:"tags" binding:"required"`
}

// SetFileTags godoc
// @Summary Replace file tags (editor permission required)
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Param id path string true "File UUID"
// @Param body body driveTagsRequest true "Up to 20 tags; empty array clears tags"
// @Success 200 {object} map[string]interface{}
// @Router /drive/files/{id}/tags [put]
func (h *DriveHandler) SetFileTags(c *gin.Context) {
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req driveTagsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("tags array is required"))
		return
	}
	tags, err := h.drive.SetFileTags(c.Request.Context(), h.getUserID(c), id, *req.Tags)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"tags": tags})
}

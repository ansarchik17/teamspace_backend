package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
)

// GetFileTypes godoc
// @Summary Get file types
// @Description Get list of available file types with count for current user
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Success 200 {array} repository.FileTypeInfo
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/file-types [get]
func (h *DriveHandler) GetFileTypes(c *gin.Context) {
	userID := h.getUserID(c)
	if userID == 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}

	fileTypes, err := h.drive.GetFileTypes(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if fileTypes == nil {
		fileTypes = []repository.FileTypeInfo{}
	}

	respondJSON(c, http.StatusOK, fileTypes)
}

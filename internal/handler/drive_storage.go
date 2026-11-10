package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/service"
)

// GetDriveStorage godoc
// @Summary Get Drive quota, usage, free space and upload limits
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.DriveStorage
// @Failure 401 {object} map[string]interface{}
// @Router /drive/storage [get]
func (h *DriveHandler) GetDriveStorage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	storage, err := h.drive.GetDriveStorage(c.Request.Context(), h.getUserID(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, storage)
}

func (h *DriveHandler) parseDriveUpload(c *gin.Context) bool {
	if c.Request.ContentLength > service.DriveMaxRequestBytes {
		h.respondError(c, apperrors.DriveUploadTooBig(service.DriveMaxRequestBytes))
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.DriveMaxRequestBytes)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.respondError(c, apperrors.DriveUploadTooBig(service.DriveMaxRequestBytes))
		} else {
			h.respondError(c, apperrors.BadRequest("invalid multipart upload"))
		}
		return false
	}
	return true
}

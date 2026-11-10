package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

// RevokePublicLink godoc
// @Summary Revoke a public link immediately for subsequent requests
// @Tags drive
// @Security BearerAuth
// @Param token path string true "Access token"
// @Success 204
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/share/link/{token} [delete]
func (h *DriveHandler) RevokePublicLink(c *gin.Context) {
	setPublicLinkHeaders(c)
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}
	if err = h.drive.RevokePublicLink(c.Request.Context(), h.getUserID(c), token); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Status(204)
}

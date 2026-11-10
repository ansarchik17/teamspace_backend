package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
)

// OfficeCollaborationUsers godoc
// @Summary Resolve editor participant names and avatars
// @Description Uses a file-scoped collaboration token embedded in the editor HTML. Only users with current access to that file are returned; maximum 100 IDs.
// @Tags docs
// @Produce json
// @Param file_id query string true "File UUID"
// @Param token query string true "Collaboration token"
// @Param ids query string true "Comma-separated user IDs"
// @Success 200 {array} domain.OfficeUser
// @Failure 400,401,403,409 {object} map[string]interface{}
// @Router /drive/onlyoffice/collaboration [get]
func (h *DriveHandler) OfficeCollaborationUsers(c *gin.Context) {
	claims, id, ok := h.officeCollaborationClaims(c)
	if !ok {
		return
	}
	ids := []string{}
	if raw := c.Query("ids"); raw != "" {
		ids = strings.Split(raw, ",")
	}
	users, err := h.drive.OfficeUsers(c.Request.Context(), claims.UserID, id, claims.DocumentKey, ids)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, users)
}

// OfficePresence godoc
// @Summary Heartbeat or leave an editor tab
// @Description Uses a signed collaboration token for one file, user and tab. Only edit sessions may heartbeat; inactive tabs expire after 90 seconds.
// @Tags docs
// @Accept json
// @Produce json
// @Param file_id query string true "File UUID"
// @Param token query string true "Collaboration token"
// @Param body body domain.OfficePresenceRequest true "Presence action"
// @Success 204
// @Failure 400,401,403,409 {object} map[string]interface{}
// @Router /drive/onlyoffice/collaboration [post]
func (h *DriveHandler) OfficePresence(c *gin.Context) {
	claims, _, ok := h.officeCollaborationClaims(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var req domain.OfficePresenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid presence action"))
		return
	}
	if err := h.drive.UpdateOfficePresence(c.Request.Context(), claims, req.Action); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DriveHandler) officeCollaborationClaims(c *gin.Context) (*service.OnlyOfficeTokenClaims, uuid.UUID, bool) {
	setPublicLinkHeaders(c)
	id, err := uuid.Parse(c.Query("file_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file_id"))
		return nil, id, false
	}
	claims, err := service.ValidateOnlyOfficeCollaborationToken(c.Query("token"), h.jwtSecret)
	if err != nil || claims.FileID != id.String() {
		h.respondError(c, apperrors.Unauthorized("invalid collaboration token"))
		return nil, id, false
	}
	return claims, id, true
}

// OfficeParticipants godoc
// @Summary List active editors of a Drive document
// @Description Deduplicated by user across tabs. Only active session keys, recent heartbeats and users with current editor rights are included. Presence expires after 90 seconds without a heartbeat.
// @Tags docs
// @Security BearerAuth
// @Produce json
// @Param id path string true "Drive file UUID"
// @Success 200 {array} domain.OfficeParticipant
// @Failure 400,401,403,404 {object} map[string]interface{}
// @Router /docs/files/{id}/participants [get]
func (h *DriveHandler) OfficeParticipants(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}
	users, err := h.drive.OfficeParticipants(c.Request.Context(), h.getUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, users)
}

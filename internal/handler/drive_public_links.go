package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
)

func parsePublicLinkTarget(c *gin.Context) (service.PublicLinkTarget, error) {
	target := service.PublicLinkTarget{}
	raw, kind := c.Query("resource_id"), c.Query("resource_type")
	if raw == "" && kind == "" {
		return target, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || (kind != "file" && kind != "folder") {
		return target, apperrors.BadRequest("invalid public link target")
	}
	return service.PublicLinkTarget{ID: id, Type: domain.ResourceType(kind)}, nil
}

func setPublicLinkHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
}

// ListPublicLinks godoc
// @Summary List public links for a managed resource, including expired links
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param resource_type query string true "file or folder"
// @Param resource_id query string true "Resource UUID"
// @Param limit query int false "Page size (1-100, default 50)"
// @Param offset query int false "Offset (default 0)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /drive/share/link [get]
func (h *DriveHandler) ListPublicLinks(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, err := uuid.Parse(c.Query("resource_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource_id uuid"))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid limit"))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid offset"))
		return
	}
	links, err := h.drive.ListPublicLinks(c.Request.Context(), h.getUserID(c), domain.ResourceType(c.Query("resource_type")), id, limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	items := make([]gin.H, 0, len(links))
	for _, l := range links {
		items = append(items, gin.H{"token": l.Token, "url": fmt.Sprintf("%s/web/drive/share/%s", h.publicURL, l.Token), "resource_type": l.ResourceType, "resource_id": l.ResourceID, "role": l.Role, "expires_at": l.ExpiresAt, "created_at": l.CreatedAt, "created_by": l.CreatedBy})
	}
	respondJSON(c, http.StatusOK, gin.H{"items": items, "limit": limit, "offset": offset})
}

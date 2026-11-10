package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type driveFavoriteReq struct {
	ResourceType domain.ResourceType `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID   uuid.UUID           `json:"resource_id" binding:"required"`
}

// GetWorkspaceSummary godoc
// @Summary Get Drive counts and storage allocation
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.DriveSummary
// @Failure 401 {object} map[string]interface{}
// @Router /drive/summary [get]
func (h *DriveHandler) GetWorkspaceSummary(c *gin.Context) {
	summary, err := h.drive.GetWorkspaceSummary(c.Request.Context(), h.getUserID(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, summary)
}

func (h *DriveHandler) ListFavorites(c *gin.Context) {
	limit, offset := parsePagination(c)
	items, err := h.drive.ListFavorites(c.Request.Context(), h.getUserID(c), limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *DriveHandler) AddFavorite(c *gin.Context) {
	var req driveFavoriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid favorite payload"))
		return
	}
	if err := h.drive.AddFavorite(c.Request.Context(), h.getUserID(c), req.ResourceType, req.ResourceID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, gin.H{"status": "favorite"})
}

func (h *DriveHandler) RemoveFavorite(c *gin.Context) {
	resourceType := domain.ResourceType(c.Param("type"))
	if resourceType != domain.ResourceFile && resourceType != domain.ResourceFolder {
		h.respondError(c, apperrors.BadRequest("invalid resource type"))
		return
	}
	resourceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource id"))
		return
	}
	if err := h.drive.RemoveFavorite(c.Request.Context(), h.getUserID(c), resourceType, resourceID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "removed"})
}

func (h *DriveHandler) ListTrash(c *gin.Context) {
	limit, offset := parsePagination(c)
	items, err := h.drive.ListTrash(c.Request.Context(), h.getUserID(c), limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *DriveHandler) RestoreTrashItem(c *gin.Context) {
	resourceType := domain.ResourceType(c.Param("type"))
	if resourceType != domain.ResourceFile && resourceType != domain.ResourceFolder {
		h.respondError(c, apperrors.BadRequest("invalid resource type"))
		return
	}
	resourceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource id"))
		return
	}
	if err := h.drive.RestoreTrashItem(c.Request.Context(), h.getUserID(c), resourceType, resourceID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "restored"})
}

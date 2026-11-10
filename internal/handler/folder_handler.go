package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	_ "github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type FolderHandler struct {
	folderService *service.FolderService
	logger        *zap.Logger
}

func NewFolderHandler(folderService *service.FolderService, logger *zap.Logger) *FolderHandler {
	return &FolderHandler{
		folderService: folderService,
		logger:        logger.With(zap.String("handler", "folder")),
	}
}

type CreateFolderRequest struct {
	Name string `json:"name" binding:"required"`
}

type UpdateFolderRequest struct {
	Name string `json:"name" binding:"required"`
}

type AddChatToFolderRequest struct {
	ChatID   string `json:"chat_id" binding:"required"`
	ChatType string `json:"chat_type" binding:"required,oneof=conversation group"`
}

// CreateFolder godoc
// @Summary Create chat folder
// @Tags folders
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body CreateFolderRequest true "Request"
// @Success 200 {object} domain.ChatFolder
// @Router /folders [post]
func (h *FolderHandler) CreateFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	folder, err := h.folderService.CreateFolder(c.Request.Context(), userID, req.Name)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, folder)
}

// GetFolders godoc
// @Summary Get user folders
// @Tags folders
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.ChatFolderView
// @Router /folders [get]
func (h *FolderHandler) GetFolders(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	folders, err := h.folderService.GetUserFolders(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, folders)
}

// UpdateFolder godoc
// @Summary Update chat folder
// @Tags folders
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Folder ID"
// @Param request body UpdateFolderRequest true "Request"
// @Success 200 {object} map[string]string
// @Router /folders/{id} [put]
func (h *FolderHandler) UpdateFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder ID"))
		return
	}

	var req UpdateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.folderService.UpdateFolder(c.Request.Context(), folderID, userID, req.Name); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "folder updated")})
}

// DeleteFolder godoc
// @Summary Delete chat folder
// @Tags folders
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder ID"
// @Success 200 {object} map[string]string
// @Router /folders/{id} [delete]
func (h *FolderHandler) DeleteFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder ID"))
		return
	}

	if err := h.folderService.DeleteFolder(c.Request.Context(), folderID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "folder deleted")})
}

// GetFolder godoc
// @Summary Get folder details with chats
// @Tags folders
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder ID"
// @Success 200 {object} domain.ChatFolderView
// @Router /folders/{id} [get]
func (h *FolderHandler) GetFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder ID"))
		return
	}

	folder, err := h.folderService.GetFolder(c.Request.Context(), folderID, userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, folder)
}

// AddChat godoc
// @Summary Add chat to folder
// @Tags folders
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Folder ID"
// @Param request body AddChatToFolderRequest true "Request"
// @Success 200 {object} map[string]string
// @Router /folders/{id}/chats [post]
func (h *FolderHandler) AddChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder ID"))
		return
	}

	var req AddChatToFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	chatID, err := uuid.Parse(req.ChatID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	if err := h.folderService.AddChatToFolder(c.Request.Context(), folderID, userID, chatID, req.ChatType); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "chat added to folder")})
}

// RemoveChat godoc
// @Summary Remove chat from folder
// @Tags folders
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder ID"
// @Param chat_id path string true "Chat ID"
// @Success 200 {object} map[string]string
// @Router /folders/{id}/chats/{chat_id} [delete]
func (h *FolderHandler) RemoveChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder ID"))
		return
	}

	chatID, err := uuid.Parse(c.Param("chat_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	if err := h.folderService.RemoveChatFromFolder(c.Request.Context(), folderID, userID, chatID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "chat removed from folder")})
}

func (h *FolderHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

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

type PinnedHandler struct {
	pinnedService *service.PinnedService
	logger        *zap.Logger
}

func NewPinnedHandler(pinnedService *service.PinnedService, logger *zap.Logger) *PinnedHandler {
	return &PinnedHandler{
		pinnedService: pinnedService,
		logger:        logger.With(zap.String("handler", "pinned")),
	}
}

// PinChatRequest represents the request body for pinning/unpinning a chat
type PinChatRequest struct {
	ChatID   string `json:"chat_id" binding:"required"`
	ChatType string `json:"chat_type" binding:"required,oneof=personal group channel"`
}

// PinMessageRequest represents the request body for pinning a message
type PinMessageRequest struct {
	ChatID    string `json:"chat_id" binding:"required"`
	ChatType  string `json:"chat_type" binding:"required,oneof=personal group channel"`
	MessageID string `json:"message_id" binding:"required"`
}

// PinChat godoc
// @Summary Pin chat
// @Description Pin a chat for current user
// @Tags pinned
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body PinChatRequest true "Pin request"
// @Success 200 {object} map[string]interface{}
// @Router /chats/pin [post]
func (h *PinnedHandler) PinChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req PinChatRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	chatID, err := uuid.Parse(req.ChatID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	if err := h.pinnedService.PinChat(c.Request.Context(), userID, chatID, req.ChatType); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "chat pinned successfully")})
}

// UnpinChat godoc
// @Summary Unpin chat
// @Description Unpin a chat for current user
// @Tags pinned
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body PinChatRequest true "Unpin request"
// @Success 200 {object} map[string]interface{}
// @Router /chats/unpin [post]
func (h *PinnedHandler) UnpinChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req PinChatRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	chatID, err := uuid.Parse(req.ChatID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	if err := h.pinnedService.UnpinChat(c.Request.Context(), userID, chatID, req.ChatType); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "chat unpinned successfully")})
}

// GetPinnedChats godoc
// @Summary Get pinned chats
// @Description Get all pinned chats for current user
// @Tags pinned
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.PinnedChat
// @Router /chats/pinned [get]
func (h *PinnedHandler) GetPinnedChats(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	pinnedChats, err := h.pinnedService.GetPinnedChats(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"pinned_chats": pinnedChats,
	})
}

// PinMessage godoc
// @Summary Pin message
// @Description Pin a message in chat
// @Tags pinned
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body PinMessageRequest true "Pin message request"
// @Success 200 {object} map[string]interface{}
// @Router /messages/pin [post]
func (h *PinnedHandler) PinMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req PinMessageRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	chatID, err := uuid.Parse(req.ChatID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	messageID, err := uuid.Parse(req.MessageID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	if err := h.pinnedService.PinMessage(c.Request.Context(), chatID, req.ChatType, messageID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "message pinned successfully")})
}

// UnpinMessage godoc
// @Summary Unpin message
// @Description Unpin a message
// @Tags pinned
// @Security BearerAuth
// @Param message_id path string true "Message ID"
// @Param chat_id query string true "Chat ID"
// @Param chat_type query string true "Chat Type"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /messages/{message_id}/unpin [post]
func (h *PinnedHandler) UnpinMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageIDStr := c.Param("message_id")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	// We need chatID and chatType - get from query params
	chatIDStr := c.Query("chat_id")
	chatType := c.Query("chat_type")

	if chatIDStr == "" || chatType == "" {
		h.respondError(c, apperrors.BadRequest("chat_id and chat_type are required"))
		return
	}

	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	if err := h.pinnedService.UnpinMessage(c.Request.Context(), chatID, chatType, messageID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "message unpinned successfully")})
}

// GetPinnedMessages godoc
// @Summary Get pinned messages
// @Description Get all pinned messages for a chat
// @Tags pinned
// @Security BearerAuth
// @Param chat_id query string true "Chat ID"
// @Param chat_type query string true "Chat Type"
// @Produce json
// @Success 200 {array} domain.PinnedMessage
// @Router /messages/pinned [get]
func (h *PinnedHandler) GetPinnedMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	chatIDStr := c.Query("chat_id")
	chatType := c.Query("chat_type")

	if chatIDStr == "" || chatType == "" {
		h.respondError(c, apperrors.BadRequest("chat_id and chat_type are required"))
		return
	}

	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat ID"))
		return
	}

	pinnedMessages, err := h.pinnedService.GetPinnedMessages(c.Request.Context(), chatID, chatType, userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"pinned_messages": pinnedMessages,
	})
}

func (h *PinnedHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

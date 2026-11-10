package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type ChatCommandHandler struct {
	service *service.ChatCommandService
	logger  *zap.Logger
}

func NewChatCommandHandler(service *service.ChatCommandService, logger *zap.Logger) *ChatCommandHandler {
	return &ChatCommandHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "chat_command")),
	}
}

func (h *ChatCommandHandler) ListCommands(c *gin.Context) {
	respondJSON(c, http.StatusOK, gin.H{
		"commands": h.service.ListDefinitions(),
	})
}

func (h *ChatCommandHandler) GetKanbanContext(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var boardID *uuid.UUID
	if raw := c.Query("board_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid board_id"))
			return
		}
		boardID = &parsed
	}

	resp, err := h.service.GetKanbanContext(c.Request.Context(), userID, boardID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

func (h *ChatCommandHandler) Execute(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	senderSessionKey := buildRealtimeSessionKey(c)

	var req service.ChatCommandExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.Execute(c.Request.Context(), userID, req, senderSessionKey)
	if err != nil {
		h.logger.Warn("chat command execution failed", zap.Error(err), zap.Int64("user_id", userID), zap.String("command_id", req.CommandID))
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	status := http.StatusOK
	if resp != nil && resp.Status == service.ChatCommandStatusExecuted {
		status = http.StatusCreated
	}
	respondJSON(c, status, resp)
}

func (h *ChatCommandHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

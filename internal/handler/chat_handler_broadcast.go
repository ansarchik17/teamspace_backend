package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

// BroadcastMessage godoc
// @Summary Broadcast message to multiple users
// @Description Send the same message to multiple users via individual conversations
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body service.BroadcastMessageRequest true "Broadcast request"
// @Success 200 {object} service.BroadcastMessageResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /conversations/broadcast [post]
func (h *ChatHandler) BroadcastMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req service.BroadcastMessageRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	response, err := h.chatService.BroadcastMessage(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

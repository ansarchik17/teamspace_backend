package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type unifiedChatsService interface {
	GetUnifiedChatsTimed(context.Context, int64, int, int) ([]*domain.ConversationView, service.UnifiedChatsTiming, error)
}

type UnifiedHandler struct {
	unifiedService unifiedChatsService
	logger         *zap.Logger
}

func NewUnifiedHandler(unifiedService unifiedChatsService, logger *zap.Logger) *UnifiedHandler {
	return &UnifiedHandler{
		unifiedService: unifiedService,
		logger:         logger.With(zap.String("handler", "unified")),
	}
}

// GetUnifiedChats godoc
// @Summary Get unified chats
// @Description Get all chats (personal and groups) sorted by pinned and activity
// @Tags chats
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.ConversationView
// @Router /chats/unified [get]
func (h *UnifiedHandler) GetUnifiedChats(c *gin.Context) {
	// Get user ID from context
	userID := middleware.MustGetUserID(c)

	limit := 0
	if rawLimit, ok := c.GetQuery("limit"); ok {
		if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	offset := 0
	if rawOffset, ok := c.GetQuery("offset"); ok {
		if parsedOffset, err := strconv.Atoi(rawOffset); err == nil && parsedOffset > 0 {
			offset = parsedOffset
		}
	}

	chats, timing, err := h.unifiedService.GetUnifiedChatsTimed(c.Request.Context(), userID, limit, offset)
	// These are application durations (including pool waits), not pure SQL timings.
	// Authentication, serialization, proxy and network time are not included.
	c.Header("Server-Timing", fmt.Sprintf(
		"personal;dur=%.2f, groups;dur=%.2f, channels;dur=%.2f, pinned;dur=%.2f, fetch;dur=%.2f, app;dur=%.2f",
		float64(timing.Personal.Microseconds())/1000,
		float64(timing.Groups.Microseconds())/1000,
		float64(timing.Channels.Microseconds())/1000,
		float64(timing.Pinned.Microseconds())/1000,
		float64(timing.Fetch.Microseconds())/1000,
		float64(timing.Total.Microseconds())/1000,
	))
	if err != nil {
		middleware.RespondAppError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, chats)
}

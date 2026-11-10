package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	boardws "github.com/west6ide/workspace-backend/internal/whiteboard"
	"go.uber.org/zap"
)

var whiteboardUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type WhiteboardHandler struct {
	service *service.WhiteboardService
	manager *boardws.Manager
	logger  *zap.Logger
}

func NewWhiteboardHandler(service *service.WhiteboardService, manager *boardws.Manager, logger *zap.Logger) *WhiteboardHandler {
	return &WhiteboardHandler{
		service: service,
		manager: manager,
		logger:  logger.With(zap.String("handler", "whiteboard")),
	}
}

// HandleWS godoc
// @Summary Whiteboard WebSocket connection
// @Description Upgrades the request to WebSocket for collaborative whiteboard events. Authentication is inherited from the protected route and supports the same Authorization header, access_token cookie, and token/access_token query param flow already used by the project for WebSocket auth.
// @Description Message types: init, draw, undo, clear, cursor, ping, pong, error.
// @Tags whiteboards
// @Security BearerAuth
// @Param roomId path string true "Board ID (UUID)"
// @Param token query string false "Access token (alternative to header/cookie)"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 401 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Router /whiteboards/ws/{roomId} [get]
func (h *WhiteboardHandler) HandleWS(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		middleware.RespondAppError(c, apperrors.BadRequest("Неверный ID доски"))
		return
	}

	if err := h.service.CanConnect(c.Request.Context(), userID, roomID); err != nil {
		middleware.RespondAppError(c, apperrors.ToAppError(err))
		return
	}

	room, err := h.manager.GetOrCreateRoom(c.Request.Context(), roomID)
	if err != nil {
		h.logger.Error("Failed to prepare whiteboard room", zap.String("room_id", roomID.String()), zap.Error(err))
		middleware.RespondAppError(c, apperrors.Internal("Не удалось подготовить whiteboard room"))
		return
	}

	conn, err := whiteboardUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade whiteboard connection", zap.String("room_id", roomID.String()), zap.Error(err))
		return
	}

	client := boardws.NewClient(userID, roomID, conn, room, h.logger, 1<<20)
	room.Register(client)

	h.logger.Info("Whiteboard websocket connected", zap.Int64("user_id", userID), zap.String("room_id", roomID.String()))

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go client.WritePump(ctx)
	client.ReadPump(ctx)

	h.logger.Info("Whiteboard websocket disconnected", zap.Int64("user_id", userID), zap.String("room_id", roomID.String()), zap.Time("closed_at", time.Now().UTC()))
}

package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	ws "github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024, // Increased from 1KB to 16KB
	WriteBufferSize: 16 * 1024, // Increased from 1KB to 16KB
	CheckOrigin: func(r *http.Request) bool {
		// TODO: Implement proper origin checking
		return true
	},
}

type WebSocketHandler struct {
	hub           *ws.Hub
	statusService *service.StatusService
	logger        *zap.Logger
}

func NewWebSocketHandler(hub *ws.Hub, statusService *service.StatusService, logger *zap.Logger) *WebSocketHandler {
	return &WebSocketHandler{
		hub:           hub,
		statusService: statusService,
		logger:        logger.With(zap.String("handler", "websocket")),
	}
}

// HandleConnection handles WebSocket connection
// @Summary WebSocket connection
// @Description Establish WebSocket connection for real-time messaging
// @Tags websocket
// @Security BearerAuth
// @Param token query string false "Access token (alternative to header)"
// @Success 101 {string} string "Switching Protocols"
// @Router /ws [get]
func (h *WebSocketHandler) HandleConnection(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	sessionKey := buildRealtimeSessionKey(c)

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade connection", zap.Error(err))
		return
	}

	// Create client
	client := ws.NewClient(userID, sessionKey, conn, h.hub, h.logger)

	// Register client with hub
	h.hub.Register(client)

	h.logger.Info("WebSocket connection established", zap.Int64("user_id", userID))

	// Broadcast online status to interested users
	ctx := c.Request.Context()
	go h.statusService.BroadcastUserOnline(ctx, userID)
	// Send initial statuses directly to client to avoid race condition with hub registration
	go func() {
		onlineUsers, err := h.statusService.GetInitialOnlineUsers(context.Background(), userID)
		if err == nil && len(onlineUsers) > 0 {
			msg := &ws.Message{
				Type:      "initial_online_list",
				To:        userID,
				Content:   onlineUsers,
				Timestamp: time.Now().Unix(),
			}
			// Send directly to client channel
			client.EnqueueMessage(msg)
		}
	}()

	// Start client pumps
	go client.WritePump(ctx)

	// ReadPump blocks until connection closes
	client.ReadPump(ctx)

	// When ReadPump exits, user disconnected
	// Broadcast offline status to interested users
	h.statusService.BroadcastUserOffline(context.Background(), userID)
}

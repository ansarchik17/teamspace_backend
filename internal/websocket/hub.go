package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	gws "github.com/gorilla/websocket"
	"github.com/west6ide/workspace-backend/internal/telemetry"
	"go.uber.org/zap"
)

// Hub maintains active WebSocket connections
type Hub struct {
	clients    map[int64]map[*Client]bool // userID -> Set of Clients
	mu         sync.RWMutex
	broadcast  chan *Message
	register   chan *Client
	unregister chan *Client
	logger     *zap.Logger
	telemetry  *telemetry.CallTelemetry
	// KanbanTaskMovedResolver validates a persisted move and returns board recipients.
	KanbanTaskMovedResolver func(ctx context.Context, userID int64, boardID, taskID, fromColumnID, toColumnID uuid.UUID) ([]int64, error)
	userUpdater             UserStatusUpdater
}

type UserStatusUpdater interface {
	UpdateLastSeen(ctx context.Context, userID int64, seenAt time.Time) error
}

// Message represents a WebSocket message
type Message struct {
	ID   *uuid.UUID `json:"id,omitempty"`
	Type string     `json:"type"` // Notification, call, presence, or kanban event type.
	From int64      `json:"from"`
	To   int64      `json:"to,omitempty"`

	BoardID      *uuid.UUID        `json:"board_id,omitempty"`
	TaskID       *uuid.UUID        `json:"task_id,omitempty"`
	ColumnID     *uuid.UUID        `json:"column_id,omitempty"`
	FromColumnID *uuid.UUID        `json:"from_column_id,omitempty"`
	ToColumnID   *uuid.UUID        `json:"to_column_id,omitempty"`
	Title        string            `json:"title,omitempty"`
	Body         string            `json:"body,omitempty"`
	Category     string            `json:"category,omitempty"`
	IsTransient  bool              `json:"is_transient,omitempty"`
	Content      interface{}       `json:"content,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`

	Timestamp         int64  `json:"timestamp"`
	ExcludeSessionKey string `json:"-"`
}

// NewHub creates a new WebSocket hub
func NewHub(logger *zap.Logger, userUpdater UserStatusUpdater, callTelemetry *telemetry.CallTelemetry) *Hub {
	return &Hub{
		clients:     make(map[int64]map[*Client]bool),
		broadcast:   make(chan *Message, 256),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		logger:      logger.With(zap.String("component", "websocket_hub")),
		telemetry:   callTelemetry,
		userUpdater: userUpdater,
	}
}

// Run starts the hub
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info("WebSocket hub started")

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("WebSocket hub shutting down")
			return

		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastMessage(message)
		}
	}
}

// Register registers a new client (public method)
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister unregisters a client (public method)
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// RegisterClient registers a new client
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Initialize map for user if not exists
	if _, exists := h.clients[client.UserID]; !exists {
		h.clients[client.UserID] = make(map[*Client]bool)
	}

	h.clients[client.UserID][client] = true

	h.logger.Info("Client registered",
		zap.Int64("user_id", client.UserID),
		zap.Int("user_active_sessions", len(h.clients[client.UserID])),
		zap.Int("total_connected_users", len(h.clients)),
	)
}

// unregisterClient removes a client
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	userClients, exists := h.clients[client.UserID]
	if !exists {
		return
	}

	if _, ok := userClients[client]; ok {
		delete(userClients, client)
		client.sendMu.Lock()
		close(client.send)
		client.sendMu.Unlock()

		h.logger.Info("Client unregistered",
			zap.Int64("user_id", client.UserID),
			zap.Int("remaining_sessions", len(userClients)),
		)

		// If user has no more sessions, remove from main map
		if len(userClients) == 0 {
			delete(h.clients, client.UserID)
			// Update last seen time (async but database-first) and broadcast offline status
			go func(uid int64, offlineTime time.Time) {
				if h.userUpdater != nil {
					dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
					if err := h.userUpdater.UpdateLastSeen(dbCtx, uid, offlineTime); err != nil {
						h.logger.Error("Failed to update last seen on unregister", zap.Error(err), zap.Int64("user_id", uid))
					}
					dbCancel()
				}
				// Broadcast status update after DB update has completed
				title, body := buildStatusNotificationText(uid, "offline")
				h.Broadcast(&Message{
					Type:        "user_status",
					From:        uid,
					Title:       title,
					Body:        body,
					Category:    "user_status",
					IsTransient: true,
					Content: map[string]interface{}{
						"user_id":              uid,
						"status":               "offline",
						"last_seen_at":         offlineTime,
						"is_last_seen":         true, // Flag for frontend to use last_seen_at
						"display_title":        title,
						"display_content":      body,
						"is_transient":         true,
						"should_show_in_inbox": false,
					},
					Timestamp: offlineTime.Unix(),
				})
			}(client.UserID, time.Now())
		}
	}
}

// broadcastMessage broadcasts message to recipient(s)
func (h *Hub) broadcastMessage(message *Message) {
	enrichRealtimeMessage(message)

	h.mu.RLock()
	defer h.mu.RUnlock()

	// Send to specific user
	if message.To > 0 {
		if userClients, exists := h.clients[message.To]; exists {
			for client := range userClients {
				if shouldSkipClient(client, message) {
					continue
				}

				select {
				case client.send <- message:
					// Log only once per user or debug level
					h.logger.Debug("Message sent",
						zap.Int64("from", message.From),
						zap.Int64("to", message.To),
						zap.String("type", message.Type),
					)
				default:
					h.logger.Warn("Client send buffer full",
						zap.Int64("user_id", message.To),
					)
					h.recordBufferFull("direct_send", message.To, message.Type, len(client.send), cap(client.send))
					// We might want to close connection here if it's dead, but unregister handles it
				}
			}
		}
		return
	}

	// Broadcast to all connected clients (fallback / system messages)
	for userID, userClients := range h.clients {
		for client := range userClients {
			if shouldSkipClient(client, message) {
				continue
			}

			select {
			case client.send <- message:
			default:
				h.logger.Warn("Client send buffer full during broadcast",
					zap.Int64("user_id", userID),
				)
				h.recordBufferFull("broadcast", userID, message.Type, len(client.send), cap(client.send))
			}
		}
	}
}

// SendToUser sends a message to a specific user (all their devices)
func (h *Hub) SendToUser(userID int64, message *Message) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	userClients, exists := h.clients[userID]
	if !exists {
		h.logger.Debug("User not connected", zap.Int64("user_id", userID))
		return 0
	}

	sentCount := 0
	failedCount := 0
	for client := range userClients {
		if shouldSkipClient(client, message) {
			continue
		}

		select {
		case client.send <- message:
			sentCount++
		default:
			failedCount++
			h.logger.Warn("Failed to send message, buffer full",
				zap.Int64("user_id", userID),
				zap.String("message_type", message.Type),
				zap.Int("buffer_size", cap(client.send)),
				zap.Int("current_queue", len(client.send)),
			)
			h.recordBufferFull("send_to_user", userID, message.Type, len(client.send), cap(client.send))
		}
	}

	if sentCount > 0 {
		h.logger.Debug("Message sent to user devices",
			zap.Int64("user_id", userID),
			zap.Int("devices_count", sentCount),
			zap.Int("failed_count", failedCount),
			zap.String("type", message.Type),
		)
	} else if failedCount > 0 {
		h.logger.Warn("Failed to send message to all user devices",
			zap.Int64("user_id", userID),
			zap.Int("total_devices", len(userClients)),
			zap.String("message_type", message.Type),
		)
	}
	return sentCount
}

// CloseUserSessionsBySessionKeys force-closes active websocket sessions for the user
// when their derived session keys match one of the provided values.
func (h *Hub) CloseUserSessionsBySessionKeys(userID int64, sessionKeys []string) int {
	if userID <= 0 || len(sessionKeys) == 0 {
		return 0
	}

	keySet := make(map[string]struct{}, len(sessionKeys))
	for _, key := range sessionKeys {
		if key == "" {
			continue
		}
		keySet[key] = struct{}{}
	}
	if len(keySet) == 0 {
		return 0
	}

	h.mu.RLock()
	userClients, exists := h.clients[userID]
	if !exists {
		h.mu.RUnlock()
		return 0
	}

	targets := make([]*Client, 0, len(userClients))
	for client := range userClients {
		if client == nil || client.SessionKey == "" {
			continue
		}
		if _, ok := keySet[client.SessionKey]; ok {
			targets = append(targets, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range targets {
		client.closeOnce.Do(func() {
			h.unregisterClient(client)
			if client.conn != nil {
				_ = client.conn.WriteControl(
					gws.CloseMessage,
					gws.FormatCloseMessage(gws.CloseNormalClosure, ""),
					time.Now().Add(writeWait),
				)
				_ = client.conn.Close()
			}
		})
	}

	return len(targets)
}

// CloseAllUserSessions force-closes all active websocket sessions for the user.
func (h *Hub) CloseAllUserSessions(userID int64) int {
	if userID <= 0 {
		return 0
	}

	h.mu.RLock()
	userClients, exists := h.clients[userID]
	if !exists {
		h.mu.RUnlock()
		return 0
	}

	targets := make([]*Client, 0, len(userClients))
	for client := range userClients {
		if client == nil {
			continue
		}
		targets = append(targets, client)
	}
	h.mu.RUnlock()

	for _, client := range targets {
		client.closeOnce.Do(func() {
			h.unregisterClient(client)
			if client.conn != nil {
				_ = client.conn.WriteControl(
					gws.CloseMessage,
					gws.FormatCloseMessage(gws.CloseNormalClosure, "Logged out"),
					time.Now().Add(writeWait),
				)
				_ = client.conn.Close()
			}
		})
	}

	return len(targets)
}

func shouldSkipClient(client *Client, message *Message) bool {
	if client == nil || message == nil {
		return false
	}

	if message.ExcludeSessionKey == "" {
		return false
	}

	return client.SessionKey != "" && client.SessionKey == message.ExcludeSessionKey
}

// Broadcast broadcasts a message to all connected clients
func (h *Hub) Broadcast(message *Message) {
	select {
	case h.broadcast <- message:
	default:
		h.logger.Warn("Broadcast buffer full")
		h.recordBufferFull("broadcast_queue", 0, message.Type, len(h.broadcast), cap(h.broadcast))
	}
}

// GetConnectedUsers returns list of connected user IDs
func (h *Hub) GetConnectedUsers() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	users := make([]int64, 0, len(h.clients))
	for userID := range h.clients {
		users = append(users, userID)
	}
	return users
}

// IsUserOnline checks if user is online
func (h *Hub) IsUserOnline(userID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	_, exists := h.clients[userID]
	return exists
}

// GetClientCount returns number of connected clients (total sessions)
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	count := 0
	for _, userClients := range h.clients {
		count += len(userClients)
	}
	return count
}

func (h *Hub) getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

// MarshalMessage marshals a message to JSON
func MarshalMessage(message *Message) ([]byte, error) {
	return json.Marshal(message)
}

// UnmarshalMessage unmarshals JSON to a message
func UnmarshalMessage(data []byte) (*Message, error) {
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return nil, err
	}
	return &message, nil
}

// SendToUsers broadcasts a message to a specific list of user IDs (e.g. all call participants).
func (h *Hub) SendToUsers(userIDs []int64, message *Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, uid := range userIDs {
		if userClients, exists := h.clients[uid]; exists {
			for client := range userClients {
				select {
				case client.send <- message:
				default:
					h.logger.Warn("SendToUsers: buffer full", zap.Int64("user_id", uid))
					h.recordBufferFull("send_to_users", uid, message.Type, len(client.send), cap(client.send))
				}
			}
		}
	}
}

// SendUserStatusToInterested sends user status update to specific interested users
func (h *Hub) SendUserStatusToInterested(userID int64, status string, interestedUserIDs []int64) {
	title, body := buildStatusNotificationText(userID, status)
	content := map[string]interface{}{
		"user_id":              userID,
		"status":               status,
		"display_title":        title,
		"display_content":      body,
		"is_transient":         true,
		"should_show_in_inbox": false,
	}
	if status == "offline" {
		content["last_seen_at"] = time.Now()
		content["is_last_seen"] = true
	}

	message := &Message{
		Type:        "user_status",
		From:        userID,
		Title:       title,
		Body:        body,
		Category:    "user_status",
		IsTransient: true,
		Content:     content,
		Timestamp:   h.getCurrentTimestamp(),
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	sentCount := 0
	for _, targetUserID := range interestedUserIDs {
		if userClients, exists := h.clients[targetUserID]; exists {
			for client := range userClients {
				select {
				case client.send <- message:
					sentCount++
				default:
					h.logger.Warn("Failed to send status update, buffer full",
						zap.Int64("target_user_id", targetUserID),
					)
					h.recordBufferFull("status_update", targetUserID, message.Type, len(client.send), cap(client.send))
				}
			}
		}
	}

	if sentCount > 0 {
		h.logger.Debug("User status sent to interested users",
			zap.Int64("user_id", userID),
			zap.String("status", status),
			zap.Int("recipients", sentCount),
		)
	}
}

func buildStatusNotificationText(userID int64, status string) (string, string) {
	switch status {
	case "online":
		return "Пользователь онлайн", fmt.Sprintf("Пользователь #%d сейчас в сети", userID)
	case "offline":
		return "Пользователь офлайн", fmt.Sprintf("Пользователь #%d вышел из сети", userID)
	default:
		return "Статус пользователя изменён", fmt.Sprintf("Пользователь #%d: %s", userID, status)
	}
}

func enrichRealtimeMessage(message *Message) {
	if message == nil {
		return
	}

	switch message.Type {
	case "user_status":
		if message.Category == "" {
			message.Category = "user_status"
		}
		message.IsTransient = true
		status := ""
		if content, ok := message.Content.(map[string]interface{}); ok {
			if raw, ok := content["status"].(string); ok {
				status = raw
			}
		}
		title, body := buildStatusNotificationText(message.From, status)
		if message.Title == "" {
			message.Title = title
		}
		if message.Body == "" {
			message.Body = body
		}
		message.Content = mergeContentDisplayFields(message.Content, message.Title, message.Body, true)
	}
}

func mergeContentDisplayFields(content interface{}, title, body string, isTransient bool) interface{} {
	existing, _ := content.(map[string]interface{})
	next := make(map[string]interface{}, len(existing)+4)
	for k, v := range existing {
		next[k] = v
	}
	next["display_title"] = title
	next["display_content"] = body
	next["is_transient"] = isTransient
	next["should_show_in_inbox"] = !isTransient
	return next
}

func (h *Hub) recordBufferFull(scope string, userID int64, messageType string, queueLen int, queueCap int) {
	if h.telemetry == nil {
		return
	}

	labels := map[string]string{
		"scope": scope,
	}
	if messageType != "" {
		labels["message_type"] = messageType
	}
	h.telemetry.IncrementCounter("websocket_buffer_full_total", labels)
	h.telemetry.RecordEvent("websocket_buffer_full", telemetry.TraceContext{
		UserID: userID,
	}, scope, map[string]any{
		"message_type": messageType,
		"queue_len":    queueLen,
		"queue_cap":    queueCap,
	})
}

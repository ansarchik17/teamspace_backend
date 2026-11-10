package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	// ParticipantResolver finds participants for a conversation/group
	ParticipantResolver func(ctx context.Context, conversationID uuid.UUID) ([]int64, error)
	// KanbanTaskMovedResolver validates a persisted move and returns board recipients.
	KanbanTaskMovedResolver func(ctx context.Context, userID int64, boardID, taskID, fromColumnID, toColumnID uuid.UUID) ([]int64, error)
	userUpdater             UserStatusUpdater
	// SyncHandler handles message synchronization requests
	SyncHandler MessageSyncHandler
}

// MessageSyncHandler defines the interface for handling sync requests
type MessageSyncHandler interface {
	HandleReconnection(ctx context.Context, userID int64, deviceID string, lastKnownSequence int64, limit int) (interface{}, error)
}

type UserStatusUpdater interface {
	UpdateLastSeen(ctx context.Context, userID int64, seenAt time.Time) error
}

// Message represents a WebSocket message
type Message struct {
	ID                *uuid.UUID        `json:"id,omitempty"`
	Type              string            `json:"type"` // "message", "typing", "read", "sync_request", "sync_response", etc.
	From              int64             `json:"from"`
	To                int64             `json:"to,omitempty"`
	ConversationID    *uuid.UUID        `json:"conversation_id,omitempty"`
	MessageID         *uuid.UUID        `json:"message_id,omitempty"`
	BoardID           *uuid.UUID        `json:"board_id,omitempty"`
	TaskID            *uuid.UUID        `json:"task_id,omitempty"`
	ColumnID          *uuid.UUID        `json:"column_id,omitempty"`
	FromColumnID      *uuid.UUID        `json:"from_column_id,omitempty"`
	ToColumnID        *uuid.UUID        `json:"to_column_id,omitempty"`
	Title             string            `json:"title,omitempty"`
	Body              string            `json:"body,omitempty"`
	Category          string            `json:"category,omitempty"`
	IsTransient       bool              `json:"is_transient,omitempty"`
	Content           interface{}       `json:"content,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	IsMention         bool              `json:"is_mention,omitempty"`
	Timestamp         int64             `json:"timestamp"`
	ExcludeSessionKey string            `json:"-"`
	// Sync-specific fields
	DeviceID     string `json:"device_id,omitempty"`
	LastSequence int64  `json:"last_sequence,omitempty"`
	SyncLimit    int    `json:"sync_limit,omitempty"`
}

type messageNotificationInfo interface {
	GetIDs() (uuid.UUID, uuid.UUID)
	GetSenderDisplayName() string
	GetMessageType() string
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

	// If we have a conversation ID and a resolver, send to participants
	if message.ConversationID != nil && h.ParticipantResolver != nil {
		// Use a background context or one with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		participants, err := h.ParticipantResolver(ctx, *message.ConversationID)
		cancel()

		if err == nil {
			for _, userID := range participants {
				// Don't echo back to sender (usually)
				if userID == message.From {
					continue
				}

				if userClients, exists := h.clients[userID]; exists {
					for client := range userClients {
						if shouldSkipClient(client, message) {
							continue
						}

						select {
						case client.send <- message:
						default:
							h.logger.Warn("Client send buffer full during group broadcast",
								zap.Int64("user_id", userID),
							)
							h.recordBufferFull("group_broadcast", userID, message.Type, len(client.send), cap(client.send))
						}
					}
				}
			}
			return
		} else {
			h.logger.Warn("Failed to resolve participants for broadcast",
				zap.String("conversation_id", message.ConversationID.String()),
				zap.Error(err),
			)
			// Fallthrough to global broadcast? NO.
			// If resolving failed, we probably shouldn't broadcast to EVERYONE as fallback.
			return
		}
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

// SendMessageNotification sends a new message notification with full message data
func (h *Hub) SendMessageNotification(toUserID int64, messageView interface{}, isMention bool) int {
	return h.sendMessageNotification(toUserID, messageView, isMention, "")
}

func (h *Hub) SendMessageNotificationExceptSession(toUserID int64, messageView interface{}, isMention bool, excludeSessionKey string) int {
	return h.sendMessageNotification(toUserID, messageView, isMention, excludeSessionKey)
}

func (h *Hub) sendMessageNotification(toUserID int64, messageView interface{}, isMention bool, excludeSessionKey string) int {
	// Мы приводим интерфейс к конкретному типу, чтобы достать ID для верхнего уровня
	// Это поможет мобильным разработчикам находить ID сообщения без глубокого парсинга
	var msgID *uuid.UUID
	var convID *uuid.UUID

	// Попробуем достать ID если это правильный тип (MessageView)
	var senderName string
	var messageType string
	if mv, ok := messageView.(messageNotificationInfo); ok {
		mID, cID := mv.GetIDs()
		msgID = &mID
		convID = &cID
		senderName = mv.GetSenderDisplayName()
		messageType = mv.GetMessageType()
	}

	title, body := buildMessageNotificationText(senderName, messageType, isMention)
	message := &Message{
		Type:              "new_message",
		To:                toUserID,
		MessageID:         msgID,
		ConversationID:    convID,
		Title:             title,
		Body:              body,
		Category:          "chat",
		Content:           messageView,
		IsMention:         isMention,
		Timestamp:         h.getCurrentTimestamp(),
		ExcludeSessionKey: excludeSessionKey,
	}

	return h.SendToUser(toUserID, message)
}

// SendTypingNotification sends typing indicator
func (h *Hub) SendTypingNotification(toUserID, fromUserID int64, conversationID uuid.UUID, isTyping bool) {
	title, body := buildTypingNotificationText(fromUserID, isTyping)
	message := &Message{
		Type:           "typing",
		From:           fromUserID,
		To:             toUserID,
		ConversationID: &conversationID,
		Title:          title,
		Body:           body,
		Category:       "typing",
		IsTransient:    true,
		Content: map[string]interface{}{
			"is_typing":            isTyping,
			"display_title":        title,
			"display_content":      body,
			"is_transient":         true,
			"should_show_in_inbox": false,
		},
		Timestamp: h.getCurrentTimestamp(),
	}

	h.SendToUser(toUserID, message)
}

// SendReadReceipt sends read receipt notification
func (h *Hub) SendReadReceipt(toUserID, fromUserID int64, messageID, conversationID uuid.UUID, readCount int) {
	h.SendToUser(toUserID, &Message{
		Type:           "read_receipt",
		From:           fromUserID,
		To:             toUserID,
		MessageID:      &messageID,
		ConversationID: &conversationID,
		Content: map[string]interface{}{
			"read_by_count": readCount,
			"is_read":       readCount > 0,
		},
		Timestamp: h.getCurrentTimestamp(),
	})
}

// SendDeliveryReceipt sends delivery receipt notification
func (h *Hub) SendDeliveryReceipt(toUserID, fromUserID int64, messageID, conversationID uuid.UUID, deliveredCount int) {
	h.SendToUser(toUserID, &Message{
		Type:           "delivery_receipt",
		From:           fromUserID,
		To:             toUserID,
		MessageID:      &messageID,
		ConversationID: &conversationID,
		Content: map[string]interface{}{
			"delivered_count": deliveredCount,
		},
		Timestamp: h.getCurrentTimestamp(),
	})
}

// SendUnreadCountUpdated sends unread count update for multi-device badge sync
func (h *Hub) SendUnreadCountUpdated(toUserID int64, conversationID uuid.UUID, unreadCount int) {
	h.SendToUser(toUserID, &Message{
		Type:           "unread_count_updated",
		To:             toUserID,
		ConversationID: &conversationID,
		Content: map[string]interface{}{
			"conversation_id": conversationID,
			"unread_count":    unreadCount,
		},
		Timestamp: h.getCurrentTimestamp(),
	})
}

func (h *Hub) SendMessageEditNotification(toUserID int64, messageView interface{}) {
	message := &Message{
		Type:      "message_edited",
		To:        toUserID,
		Content:   messageView, // Full updated MessageView including reply_to, sender info, etc.
		Timestamp: h.getCurrentTimestamp(),
	}

	h.SendToUser(toUserID, message)
}

func (h *Hub) SendMessageDeleteNotification(toUserID int64, conversationID, messageID uuid.UUID, isForEveryone bool, newLastMessage interface{}) {
	message := &Message{
		Type:           "message_deleted",
		To:             toUserID,
		ConversationID: &conversationID,
		MessageID:      &messageID,
		Content: map[string]interface{}{
			"is_for_everyone":  isForEveryone,
			"new_last_message": newLastMessage,
		},
		Timestamp: h.getCurrentTimestamp(),
	}

	h.SendToUser(toUserID, message)
}

func (h *Hub) SendChatHiddenNotification(toUserID int64, conversationID uuid.UUID, chatType string) {
	message := &Message{
		Type:           "chat_hidden",
		To:             toUserID,
		ConversationID: &conversationID,
		Content: map[string]interface{}{
			"chat_type": chatType,
		},
		Timestamp: h.getCurrentTimestamp(),
	}

	h.SendToUser(toUserID, message)
}

func (h *Hub) SendChatDeletedNotification(toUserID int64, conversationID uuid.UUID, chatType string) {
	message := &Message{
		Type:           "chat_deleted",
		To:             toUserID,
		ConversationID: &conversationID,
		Content: map[string]interface{}{
			"chat_type": chatType,
		},
		Timestamp: h.getCurrentTimestamp(),
	}

	h.SendToUser(toUserID, message)
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

func buildMessageNotificationText(senderName, messageType string, isMention bool) (string, string) {
	senderName = strings.TrimSpace(senderName)
	if senderName == "" {
		senderName = "Пользователь"
	}

	preview := messagePreviewText(messageType)
	if isMention {
		return "Вас упомянули", fmt.Sprintf("%s: %s", senderName, preview)
	}
	return fmt.Sprintf("Сообщение от %s", senderName), preview
}

func buildTypingNotificationText(userID int64, isTyping bool) (string, string) {
	if !isTyping {
		return "Набор текста завершён", fmt.Sprintf("Пользователь #%d перестал печатать", userID)
	}
	return "Печатает сообщение", fmt.Sprintf("Пользователь #%d набирает текст", userID)
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

func messagePreviewText(messageType string) string {
	switch messageType {
	case "text", "":
		return "Новое текстовое сообщение"
	case "image":
		return "Новое фото"
	case "file":
		return "Новый файл"
	case "audio":
		return "Новое аудио"
	case "video":
		return "Новое видео"
	case "voice":
		return "Голосовое сообщение"
	case "video_message":
		return "Видеосообщение"
	default:
		return "Новое сообщение"
	}
}

func enrichRealtimeMessage(message *Message) {
	if message == nil {
		return
	}

	switch message.Type {
	case "typing":
		if message.Category == "" {
			message.Category = "typing"
		}
		message.IsTransient = true
		isTyping := true
		if content, ok := message.Content.(map[string]interface{}); ok {
			if raw, ok := content["is_typing"].(bool); ok {
				isTyping = raw
			}
		}
		title, body := buildTypingNotificationText(message.From, isTyping)
		if message.Title == "" {
			message.Title = title
		}
		if message.Body == "" {
			message.Body = body
		}
		message.Content = mergeContentDisplayFields(message.Content, message.Title, message.Body, true)

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

// ─── Channel WebSocket events ────────────────────────────────────────────────

// Channel event type constants – use these in channel_service.go when broadcasting
const (
	// EventNewChannelPost is emitted to all online subscribers when a new post is published
	EventNewChannelPost = "new_channel_post"
	// EventChannelPostEdited is emitted when a post is edited
	EventChannelPostEdited = "channel_post_edited"
	// EventChannelPostDeleted is emitted when a post is deleted
	EventChannelPostDeleted = "channel_post_deleted"
	// EventChannelPostViewed is emitted when a view is recorded (admin dashboard use)
	EventChannelPostViewed = "channel_post_viewed"
	// EventChannelPostReaction is emitted when a reaction is added/removed
	EventChannelPostReaction = "channel_post_reaction"
)

// ─── Message Synchronization ─────────────────────────────────────────────────

// HandleSyncRequest processes a sync request from a reconnected client
func (h *Hub) HandleSyncRequest(client *Client, msg *Message) {
	if h.SyncHandler == nil {
		h.logger.Warn("Sync handler not configured")
		client.enqueue(&Message{
			Type:      "sync_error",
			To:        client.UserID,
			Content:   map[string]interface{}{"error": "sync not available"},
			Timestamp: time.Now().Unix(),
		})
		return
	}

	// Extract device ID and last sequence from the request
	deviceID := msg.DeviceID
	if deviceID == "" {
		deviceID = client.SessionKey // fallback to session key as device identifier
	}

	lastSequence := msg.LastSequence
	limit := msg.SyncLimit
	if limit <= 0 {
		limit = 100
	}

	h.logger.Info("Processing sync request",
		zap.Int64("user_id", client.UserID),
		zap.String("device_id", deviceID),
		zap.Int64("last_sequence", lastSequence),
		zap.Int("limit", limit),
	)

	// Use background context with timeout for sync operation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Call the sync handler
	syncResponse, err := h.SyncHandler.HandleReconnection(ctx, client.UserID, deviceID, lastSequence, limit)
	if err != nil {
		h.logger.Error("Failed to handle sync request",
			zap.Error(err),
			zap.Int64("user_id", client.UserID),
		)
		client.enqueue(&Message{
			Type:      "sync_error",
			To:        client.UserID,
			Content:   map[string]interface{}{"error": "message synchronization failed"},
			Timestamp: time.Now().Unix(),
		})
		return
	}

	// Send sync response back to the client
	client.enqueue(&Message{
		Type:      "sync_response",
		To:        client.UserID,
		Content:   syncResponse,
		Timestamp: time.Now().Unix(),
	})

	h.logger.Info("Sync request completed",
		zap.Int64("user_id", client.UserID),
		zap.String("device_id", deviceID),
	)
}

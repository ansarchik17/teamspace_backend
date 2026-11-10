package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	_ "github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type ChatHandler struct {
	chatService      *service.ChatService
	groupService     *service.GroupService
	unifiedAIService *service.UnifiedAIService
	kanbanService    *service.KanbanService
	calendarService  *service.CalendarService
	noteService      *service.NoteService
	logger           *zap.Logger
}

func NewChatHandler(
	chatService *service.ChatService,
	groupService *service.GroupService,
	unifiedAIService *service.UnifiedAIService,
	kanbanService *service.KanbanService,
	calendarService *service.CalendarService,
	noteService *service.NoteService,
	logger *zap.Logger,
) *ChatHandler {
	return &ChatHandler{
		chatService:      chatService,
		groupService:     groupService,
		unifiedAIService: unifiedAIService,
		kanbanService:    kanbanService,
		calendarService:  calendarService,
		noteService:      noteService,
		logger:           logger.With(zap.String("handler", "chat")),
	}
}

// CreateConversationRequest represents the request to create a conversation
type CreateConversationRequest struct {
	PeerID int64 `json:"peer_id" binding:"required"`
}

// SendMessageRequest represents the request to send a message
type SendMessageRequest struct {
	Content        string            `json:"content"`
	ContentHash    string            `json:"content_hash"` // Hash of the plaintext content for searching
	Keys           map[string]string `json:"keys"`         // Encrypted keys for multi-device E2EE
	Type           string            `json:"type" binding:"omitempty,oneof=text image video file audio voice video_message"`
	Metadata       *string           `json:"metadata,omitempty"` // JSON metadata, for example {"mentions":["username"]}
	AttachmentURL  *string           `json:"attachment_url,omitempty"`
	AttachmentType *string           `json:"attachment_type,omitempty"`
	AttachmentSize *int64            `json:"attachment_size,omitempty"`
	AttachmentName *string           `json:"attachment_name,omitempty"`
	ReplyToID      *uuid.UUID        `json:"reply_to_id,omitempty"` // ID сообщения, на которое отвечаем
	IsEphemeral    bool              `json:"is_ephemeral"`          // Flag for disappearing messages
	TTLSeconds     *int              `json:"ttl_seconds,omitempty"` // Time to live in seconds after being read
}

// EditMessageRequest represents the request to direct edit a message
type EditMessageRequest struct {
	Content string `json:"content" binding:"required"`
}

// DeleteMessagesRequest represents the request to delete multiple messages
type DeleteMessagesRequest struct {
	MessageIDs []uuid.UUID `json:"message_ids" binding:"required"`
	Scope      string      `json:"scope" binding:"omitempty,oneof=me everyone"`
}

// GetMessageModerationRules returns client-visible moderation limits.
func (h *ChatHandler) GetMessageModerationRules(c *gin.Context) {
	respondJSON(c, http.StatusOK, gin.H{
		"edit_window_seconds":                int64(service.MessageEditWindow.Seconds()),
		"delete_for_everyone_window_seconds": int64(service.MessageDeleteForEveryoneWindow.Seconds()),
		"delete_scopes":                      []string{string(service.MessageDeleteForMe), string(service.MessageDeleteForEveryone)},
	})
}

// ForwardMessagesRequest represents the request to forward messages
type ForwardMessagePayload struct {
	OriginalMessageID uuid.UUID         `json:"original_message_id" binding:"required"`
	TargetChatID      uuid.UUID         `json:"target_chat_id" binding:"required"`
	Content           string            `json:"content" binding:"required"`
	ContentHash       string            `json:"content_hash"`
	Keys              map[string]string `json:"keys"`
}

type ForwardMessagesRequest struct {
	Messages      []ForwardMessagePayload `json:"messages"`
	MessageIDs    []uuid.UUID             `json:"message_ids"`
	TargetChatIDs []uuid.UUID             `json:"target_chat_ids"`
}

// ShareItemRequest represents request to share task, event or note
type ShareItemRequest struct {
	TargetChatID uuid.UUID `json:"target_chat_id" binding:"required"`
	ItemType     string    `json:"item_type" binding:"required"` // task, event, note
	ItemID       string    `json:"item_id" binding:"required"`
	Comment      string    `json:"comment"`
}

// AddReactionRequest represents request to add reaction
type AddReactionRequest struct {
	Reaction string `json:"reaction" binding:"required"`
}

type MuteChatRequest struct {
	Mute bool `json:"mute"`
}

// CreateConversation godoc
// @Summary Create or get conversation
// @Description Create a new conversation with another user or return existing one
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body CreateConversationRequest true "Conversation request"
// @Success 200 {object} domain.Conversation
// @Failure 400 {object} map[string]interface{}
// @Router /conversations [post]
func (h *ChatHandler) CreateConversation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req CreateConversationRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if req.PeerID == userID {
		h.respondError(c, apperrors.BadRequest("cannot create conversation with yourself"))
		return
	}

	conversation, err := h.chatService.CreateOrGetConversation(c.Request.Context(), userID, req.PeerID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, conversation)
}

// GetConversations godoc
// @Summary Get user conversations
// @Description Get all conversations for current user
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit" default(20)
// @Param offset query int false "Offset" default(0)
// @Success 200 {array} domain.ConversationView
// @Router /conversations [get]
func (h *ChatHandler) GetConversations(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	conversations, err := h.chatService.GetUserConversations(ctx, userID, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"conversations": conversations,
		"count":         len(conversations),
	})
}

// SendMessage godoc
// @Summary Send message
// @Description Send a message in conversation
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param request body SendMessageRequest true "Message request"
// @Success 201 {object} domain.MessageView
// @Failure 400 {object} map[string]interface{}
// @Router /conversations/{conversation_id}/messages [post]
func (h *ChatHandler) SendMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	senderSessionKey := buildRealtimeSessionKey(c)

	conversationIDStr := c.Param("conversation_id")
	conversationID, err := uuid.Parse(conversationIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	var req SendMessageRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	attachment := &service.MessageAttachmentInput{
		URL:  req.AttachmentURL,
		Type: req.AttachmentType,
		Size: req.AttachmentSize,
		Name: req.AttachmentName,
	}

	if err := h.maybeGrantSharedNoteAccessForConversation(c.Request.Context(), req.Metadata, conversationID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	message, err := h.chatService.SendMessage(c.Request.Context(), conversationID, userID, req.Content, req.ContentHash, req.Keys, req.Type, req.Metadata, req.ReplyToID, req.IsEphemeral, req.TTLSeconds, attachment, senderSessionKey)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, message)

}

// MuteConversation godoc
// @Summary Mute or unmute conversation
// @Description Toggle push notifications for the current user in a personal conversation. Mentions still send notifications.
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param request body MuteChatRequest true "Mute request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /conversations/{conversation_id}/mute [put]
func (h *ChatHandler) MuteConversation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	conversationID, err := uuid.Parse(c.Param("conversation_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	var req MuteChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	chat, err := h.chatService.MuteConversation(c.Request.Context(), conversationID, userID, req.Mute)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":         apiMessage(c, "conversation mute setting updated"),
		"conversation_id": conversationID.String(),
		"is_muted":        req.Mute,
		"chat":            chat,
	})
}

// GetMessages godoc
// @Summary Get messages
// @Description Get messages from conversation
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param limit query int false "Limit" default(50)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /conversations/{conversation_id}/messages [get]
func (h *ChatHandler) GetMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	currentDeviceID := currentLinkedDeviceID(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	conversationID, err := uuid.Parse(c.Param("conversation_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 50
	}

	messages, err := h.chatService.GetMessages(ctx, conversationID, userID, currentDeviceID, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"messages": messages,
		"count":    len(messages),
	})
}

// GetSharedMedia godoc
// @Summary Get shared media
// @Description Get paginated image and video attachments from conversation history
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param limit query int false "Limit" default(30)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} domain.SharedMediaResponse
// @Failure 400 {object} map[string]interface{}
// @Router /conversations/{conversation_id}/shared-media [get]
func (h *ChatHandler) GetSharedMedia(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	conversationID, err := uuid.Parse(c.Param("conversation_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 30
	}

	response, err := h.chatService.GetSharedMedia(ctx, conversationID, userID, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// EditMessage godoc
// @Summary Edit message
// @Description Edit a message in conversation or group
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param message_id path string true "Message ID"
// @Param request body EditMessageRequest true "Edit request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /messages/{message_id} [put]
func (h *ChatHandler) EditMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageIDStr := c.Param("message_id")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	var req EditMessageRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	err = h.chatService.EditMessage(c.Request.Context(), messageID, userID, req.Content)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "message updated successfully")})
}

// DeleteMessage godoc
// @Summary Delete message
// @Description Delete a message
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param message_id path string true "Message ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /messages/{message_id} [delete]
func (h *ChatHandler) DeleteMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageIDStr := c.Param("message_id")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	scope := service.MessageDeleteScope(c.DefaultQuery("scope", string(service.MessageDeleteForMe)))
	if scope != service.MessageDeleteForMe && scope != service.MessageDeleteForEveryone {
		h.respondError(c, apperrors.Validation("scope must be me or everyone"))
		return
	}

	if err := h.chatService.DeleteMessage(c.Request.Context(), messageID, userID, scope); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "message deleted successfully")})
}

// DeleteMessages godoc
// @Summary Delete multiple messages
// @Description Delete multiple messages by IDs
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body DeleteMessagesRequest true "Delete request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /messages [delete]
func (h *ChatHandler) DeleteMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req DeleteMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	scope := service.MessageDeleteScope(req.Scope)
	if scope == "" {
		scope = service.MessageDeleteForMe
	}
	if err := h.chatService.DeleteMessages(c.Request.Context(), req.MessageIDs, userID, scope); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "messages deleted successfully")})
}

// DeleteConversation godoc
// @Summary Hide conversation
// @Description Hide a conversation for the current user (does not affect other participants)
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /conversations/{conversation_id} [delete]
func (h *ChatHandler) DeleteConversation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	conversationIDStr := c.Param("conversation_id")
	conversationID, err := uuid.Parse(conversationIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Теперь любой участник может скрыть чат у себя
	if err := h.chatService.HideConversation(ctx, conversationID, userID); err != nil {
		// Если не удалось скрыть обычный чат, пробуем удалить Unified AI чат
		// Так как ID уникальны, это безопасно
		if aiErr := h.unifiedAIService.DeleteChat(ctx, conversationIDStr, userID); aiErr == nil {
			respondJSON(c, http.StatusOK, gin.H{
				"message":         apiMessage(c, "conversation deleted successfully"),
				"conversation_id": conversationID.String(),
			})
			return
		}

		// Если и там не удалось - возвращаем оригинальную ошибку
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":         apiMessage(c, "conversation hidden successfully"),
		"conversation_id": conversationID.String(),
	})
}

// UnhideConversation godoc
// @Summary Unhide conversation
// @Description Restore a hidden conversation for the current user
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /conversations/{conversation_id}/unhide [post]
func (h *ChatHandler) UnhideConversation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	conversationIDStr := c.Param("conversation_id")
	conversationID, err := uuid.Parse(conversationIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := h.chatService.UnhideConversation(ctx, conversationID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":         apiMessage(c, "conversation restored successfully"),
		"conversation_id": conversationID.String(),
	})
}

// MarkAsRead godoc
// @Summary Mark message as read
// @Description Mark message as read
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param message_id path string true "Message ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /messages/{message_id}/read [post]
func (h *ChatHandler) MarkAsRead(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageIDStr := c.Param("message_id")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	if err := h.chatService.MarkAsRead(c.Request.Context(), messageID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "marked as read")})
}

// MarkAsDelivered godoc
// @Summary Mark message as delivered
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param message_id path string true "Message ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /messages/{message_id}/delivered [post]
func (h *ChatHandler) MarkAsDelivered(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	messageID, err := uuid.Parse(c.Param("message_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}
	if err := h.chatService.MarkMessageAsDelivered(c.Request.Context(), messageID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "marked as delivered")})
}

// ForwardMessages godoc
// @Summary Forward messages
// @Description Forward multiple messages to one or more chats
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ForwardMessagesRequest true "Forward request"
// @Success 200 {array} domain.MessageView
// @Failure 400 {object} map[string]interface{}
// @Router /messages/forward [post]
func (h *ChatHandler) ForwardMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	senderSessionKey := buildRealtimeSessionKey(c)

	var req ForwardMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	var (
		messages []*domain.MessageView
		err      error
	)
	if len(req.Messages) > 0 {
		inputs := make([]service.ForwardMessageInput, 0, len(req.Messages))
		for _, item := range req.Messages {
			inputs = append(inputs, service.ForwardMessageInput{
				OriginalMessageID: item.OriginalMessageID,
				TargetChatID:      item.TargetChatID,
				Content:           item.Content,
				ContentHash:       item.ContentHash,
				Keys:              item.Keys,
			})
		}
		messages, err = h.chatService.ForwardPreparedMessages(c.Request.Context(), userID, inputs, senderSessionKey)
	} else {
		messages, err = h.chatService.ForwardMessages(c.Request.Context(), userID, req.MessageIDs, req.TargetChatIDs, senderSessionKey)
	}
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, messages)
}

// GetMessageViewers godoc
// @Summary Get message viewers
// @Description Get list of users who have read the message
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param message_id path string true "Message ID"
// @Success 200 {array} domain.MessageViewer
// @Failure 400 {object} map[string]interface{}
// @Router /messages/{message_id}/viewers [get]
func (h *ChatHandler) GetMessageViewers(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageID, err := uuid.Parse(c.Param("message_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	viewers, err := h.chatService.GetMessageViewers(c.Request.Context(), userID, messageID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, viewers)
}

// ShareItem godoc
// @Summary Share task or event
// @Description Share a Kanban task or Calendar event to a chat
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ShareItemRequest true "Share request"
// @Success 201 {object} domain.MessageView
// @Failure 400 {object} map[string]interface{}
// @Router /chats/share [post]
func (h *ChatHandler) ShareItem(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	senderSessionKey := buildRealtimeSessionKey(c)

	var req ShareItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	var metadata string
	var msgType string
	var content string

	if req.Comment != "" {
		content = req.Comment
	} else {
		content = "Поделился контентом"
	}

	switch req.ItemType {
	case "task":
		taskID, err := uuid.Parse(req.ItemID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid task ID format"))
			return
		}
		task, err := h.kanbanService.GetTask(c.Request.Context(), userID, taskID)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}

		msgType = "share_task"
		metadata = fmt.Sprintf(`{"kind":"task","task_id":"%s","title":%q,"board_id":"%s"}`,
			task.ID.String(), task.Title, task.BoardID.String())

	case "event":
		eventID, err := uuid.Parse(req.ItemID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid event ID format"))
			return
		}
		event, err := h.calendarService.GetEvent(c.Request.Context(), userID, eventID)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}

		msgType = "share_event"
		location := ""
		if event.Location != nil {
			location = *event.Location
		}

		metadata = fmt.Sprintf(`{"kind":"event","event_id":"%s","title":%q,"start_time":"%s","end_time":"%s","location":%q}`,
			event.ID, event.Title, event.StartTime.Format(time.RFC3339), event.EndTime.Format(time.RFC3339), location)

	case "note":
		noteID, err := uuid.Parse(req.ItemID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid note ID format"))
			return
		}
		note, err := h.noteService.ShareNoteWithUsers(c.Request.Context(), userID, noteID, h.resolveShareTargetUsers(c, req.TargetChatID, userID))
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}

		msgType = "share_note"
		metadata = fmt.Sprintf(`{"kind":"note","note_id":"%s","title":%q}`, note.ID.String(), note.Title)

	default:
		h.respondError(c, apperrors.BadRequest("invalid item_type. must be 'task', 'event' or 'note'"))
		return
	}

	// Try finding if it's a group
	isGroup := false
	_, err := h.groupService.GetGroup(c.Request.Context(), req.TargetChatID, userID)
	if err == nil {
		isGroup = true
	}

	// Send Message
	var messageView *domain.MessageView
	var sendErr error

	metaPtr := &metadata

	if isGroup {
		messageView, sendErr = h.groupService.SendMessage(c.Request.Context(), req.TargetChatID, userID, content, "", msgType, metaPtr, nil, nil, senderSessionKey)
	} else {
		messageView, sendErr = h.chatService.SendMessage(c.Request.Context(), req.TargetChatID, userID, content, "", nil, msgType, metaPtr, nil, false, nil, nil, senderSessionKey)
	}

	if sendErr != nil {
		h.respondError(c, apperrors.ToAppError(sendErr))
		return
	}

	respondJSON(c, http.StatusCreated, messageView)
}

func (h *ChatHandler) resolveShareTargetUsers(c *gin.Context, targetChatID uuid.UUID, userID int64) []int64 {
	if _, err := h.groupService.GetGroup(c.Request.Context(), targetChatID, userID); err == nil {
		memberIDs, err := h.groupService.GetGroupMemberIDs(c.Request.Context(), targetChatID, userID)
		if err == nil {
			return memberIDs
		}
		return nil
	}

	participantIDs, err := h.chatService.GetConversationParticipantIDs(c.Request.Context(), targetChatID, userID)
	if err != nil {
		return nil
	}
	return participantIDs
}

// AddReaction godoc
// @Summary Add reaction to message
// @Description Add a reaction (emoji) to a message
// @Tags chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param message_id path string true "Message ID"
// @Param request body AddReactionRequest true "Reaction request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /messages/{message_id}/reactions [post]
func (h *ChatHandler) AddReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageID, err := uuid.Parse(c.Param("message_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	var req AddReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	reactions, err := h.chatService.AddReaction(c.Request.Context(), messageID, userID, req.Reaction)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":   apiMessage(c, "reaction added"),
		"reactions": reactions,
	})
}

// RemoveReaction godoc
// @Summary Remove reaction from message
// @Description Remove a reaction from a message
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param message_id path string true "Message ID"
// @Param reaction path string true "Reaction (URL encoded emoji)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /messages/{message_id}/reactions/{reaction} [delete]
func (h *ChatHandler) RemoveReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	messageID, err := uuid.Parse(c.Param("message_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	reaction := c.Param("reaction")
	if reaction == "" {
		h.respondError(c, apperrors.BadRequest("reaction is required"))
		return
	}

	reactions, err := h.chatService.RemoveReaction(c.Request.Context(), messageID, userID, reaction)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":   apiMessage(c, "reaction removed"),
		"reactions": reactions,
	})
}

// GetMessageThread godoc
// @Summary Get message reply tree
// @Description Returns the selected message and its visible nested replies
// @Tags chat
// @Security BearerAuth
// @Produce json
// @Param message_id path string true "Root message ID"
// @Param limit query int false "Maximum number of replies (1-200)" default(200)
// @Success 200 {object} domain.MessageThreadResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /messages/{message_id}/thread [get]
func (h *ChatHandler) GetMessageThread(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	messageID, err := uuid.Parse(c.Param("message_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid message ID"))
		return
	}

	var params struct {
		Limit int `form:"limit" binding:"omitempty,min=1,max=200"`
	}
	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("limit must be between 1 and 200"))
		return
	}

	thread, err := h.chatService.GetMessageThread(c.Request.Context(), messageID, userID, params.Limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, thread)
}

func (h *ChatHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

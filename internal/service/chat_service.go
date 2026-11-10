package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type ChatService struct {
	chatRepo            *repository.ChatRepository
	messageRepo         *repository.MessageRepository
	groupRepo           *repository.GroupRepository
	userRepo            *repository.UserRepository
	pinnedRepo          *repository.PinnedRepository
	reactionRepo        *repository.ReactionRepository
	hub                 *websocket.Hub
	notificationService *NotificationService
	gamification        *GamificationService // новое поле
	eventPublisher      EventPublisher
	logger              *zap.Logger
}

type MessageAttachmentInput struct {
	URL  *string
	Type *string
	Size *int64
	Name *string
}

type ForwardMessageInput struct {
	OriginalMessageID uuid.UUID
	TargetChatID      uuid.UUID
	Content           string
	ContentHash       string
	Keys              map[string]string
}

type chatConversationPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	CreatedBy      int64     `json:"created_by"`
	ParticipantIDs []int64   `json:"participant_ids"`
}

type chatMessagePayload struct {
	MessageID      uuid.UUID  `json:"message_id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	SenderID       int64      `json:"sender_id"`
	Type           string     `json:"type"`
	ReplyToID      *uuid.UUID `json:"reply_to_id,omitempty"`
	IsEphemeral    bool       `json:"is_ephemeral,omitempty"`
	TTLSeconds     *int       `json:"ttl_seconds,omitempty"`
	AttachmentURL  *string    `json:"attachment_url,omitempty"`
	AttachmentType *string    `json:"attachment_type,omitempty"`
}

type chatMessageEditPayload struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	EditedBy       int64     `json:"edited_by"`
}

type chatMessageDeletePayload struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         int64     `json:"user_id"`
}

type chatBulkDeletePayload struct {
	MessageIDs []uuid.UUID `json:"message_ids"`
	UserID     int64       `json:"user_id"`
}

type chatReactionPayload struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         int64     `json:"user_id"`
	Reaction       string    `json:"reaction"`
}

const maxReplyTreeMessages = 200

// messageAudience verifies that the caller can access the message container
// and returns all active recipients. The boolean reports a group chat.
func (s *ChatService) messageAudience(ctx context.Context, conversationID uuid.UUID, userID int64) ([]int64, bool, error) {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return nil, false, apperrors.InternalWithErr("failed to verify conversation participant", err)
	}
	if isParticipant {
		participants, err := s.chatRepo.GetConversationParticipants(ctx, conversationID)
		if err != nil {
			return nil, false, apperrors.InternalWithErr("failed to get conversation participants", err)
		}
		return participants, false, nil
	}

	isMember, err := s.groupRepo.IsMember(ctx, conversationID, userID)
	if err != nil {
		return nil, false, apperrors.InternalWithErr("failed to verify group member", err)
	}
	if !isMember {
		return nil, false, apperrors.NotParticipant()
	}
	members, err := s.groupRepo.GetGroupMembers(ctx, conversationID)
	if err != nil {
		return nil, true, apperrors.InternalWithErr("failed to get group members", err)
	}
	return members, true, nil
}

func (s *ChatService) canDeleteForEveryone(ctx context.Context, msg *domain.MessageView, userID int64, isGroup bool) error {
	if isGroup {
		role, err := s.groupRepo.GetMemberRole(ctx, msg.ConversationID, userID)
		if err != nil {
			return apperrors.NotGroupMember()
		}
		if role == "owner" || role == "admin" {
			return nil
		}
	}
	if msg.SenderID != userID {
		return apperrors.CannotDeleteMessage()
	}
	if !isWithinModerationWindow(msg.CreatedAt, time.Now(), MessageDeleteForEveryoneWindow) {
		return apperrors.DeleteWindowExpired()
	}
	return nil
}

func NewChatService(
	chatRepo *repository.ChatRepository,
	messageRepo *repository.MessageRepository,
	groupRepo *repository.GroupRepository,
	userRepo *repository.UserRepository,
	pinnedRepo *repository.PinnedRepository,
	reactionRepo *repository.ReactionRepository,
	hub *websocket.Hub,
	notificationService *NotificationService,
	gamification *GamificationService,
	eventPublisher EventPublisher,
	logger *zap.Logger,
) *ChatService {
	return &ChatService{
		chatRepo:            chatRepo,
		messageRepo:         messageRepo,
		groupRepo:           groupRepo,
		userRepo:            userRepo,
		pinnedRepo:          pinnedRepo,
		reactionRepo:        reactionRepo,
		hub:                 hub,
		notificationService: notificationService,
		gamification:        gamification,
		eventPublisher:      eventPublisher,
		logger:              logger.With(zap.String("service", "chat")),
	}
}

func (s *ChatService) publishConversationCreated(conversationID uuid.UUID, createdBy int64, participantIDs []int64) {
	publishEventAsync(s.logger, s.eventPublisher, "chat.conversation.created", chatConversationPayload{
		ConversationID: conversationID,
		CreatedBy:      createdBy,
		ParticipantIDs: append([]int64(nil), participantIDs...),
	}, zap.String("conversation_id", conversationID.String()))
}

func (s *ChatService) publishMessageEvent(routingKey string, message *domain.Message) {
	if message == nil {
		return
	}

	publishEventAsync(s.logger, s.eventPublisher, routingKey, chatMessagePayload{
		MessageID:      message.ID,
		ConversationID: message.ConversationID,
		SenderID:       message.SenderID,
		Type:           message.Type,
		ReplyToID:      message.ReplyToID,
		IsEphemeral:    message.IsEphemeral,
		TTLSeconds:     message.TTLSeconds,
		AttachmentURL:  message.AttachmentURL,
		AttachmentType: message.AttachmentType,
	}, zap.String("message_id", message.ID.String()))
}

// CreateOrGetConversation creates a new conversation or returns existing one
func (s *ChatService) CreateOrGetConversation(ctx context.Context, user1ID, user2ID int64) (*domain.Conversation, error) {
	// Check if conversation already exists
	conversation, err := s.chatRepo.GetConversationByParticipants(ctx, user1ID, user2ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check existing conversation", err)
	}

	// Return existing conversation
	if conversation != nil {
		return conversation, nil
	}

	// Create new conversation
	conversation, err = s.chatRepo.CreateConversation(ctx, user1ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to create conversation", err)
	}

	// Add both participants
	if err := s.chatRepo.AddParticipant(ctx, conversation.ID, user1ID); err != nil {
		return nil, apperrors.InternalWithErr("failed to add participant", err)
	}

	if err := s.chatRepo.AddParticipant(ctx, conversation.ID, user2ID); err != nil {
		return nil, apperrors.InternalWithErr("failed to add participant", err)
	}

	s.logger.Info("Conversation created",
		zap.String("conversation_id", conversation.ID.String()),
		zap.Int64("user1_id", user1ID),
		zap.Int64("user2_id", user2ID),
	)

	s.publishConversationCreated(conversation.ID, user1ID, []int64{user1ID, user2ID})

	return conversation, nil
}

// GetUserConversations gets all conversations for user
func (s *ChatService) GetUserConversations(ctx context.Context, userID int64, limit, offset int) ([]*domain.ConversationView, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	conversations, err := s.chatRepo.GetUserConversations(ctx, userID, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get conversations", err)
	}

	// Override last seen for online users
	if s.hub != nil {
		now := time.Now()
		for _, conv := range conversations {
			if conv.PeerID != nil && s.hub.IsUserOnline(*conv.PeerID) {
				conv.PeerLastSeenAt = &now
			}
		}
	}

	return conversations, nil
}

func (s *ChatService) GetConversationParticipantIDs(ctx context.Context, conversationID uuid.UUID, userID int64) ([]int64, error) {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant", err)
	}
	if !isParticipant {
		return nil, apperrors.NotParticipant()
	}

	participants, err := s.chatRepo.GetConversationParticipants(ctx, conversationID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get conversation participants", err)
	}

	return participants, nil
}

// ForwardMessages forwards multiple messages to multiple target chats (conversations or groups)
func (s *ChatService) ForwardMessages(ctx context.Context, userID int64, messageIDs []uuid.UUID, targetChatIDs []uuid.UUID, senderSessionKey string) ([]*domain.MessageView, error) {
	if len(messageIDs) == 0 || len(targetChatIDs) == 0 {
		return nil, apperrors.BadRequest("message_ids and target_chat_ids cannot be empty")
	}

	// 1. Get original messages
	originalMessages, err := s.messageRepo.GetByIDs(ctx, messageIDs)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get original messages", err)
	}

	if len(originalMessages) == 0 {
		return nil, apperrors.MessageNotFound()
	}

	var forwardedMessages []*domain.MessageView

	for _, targetChatID := range targetChatIDs {
		// 2. Check if target is a conversation or a group
		isGroup := false
		isParticipant, err := s.chatRepo.IsParticipant(ctx, targetChatID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to check participant", err)
		}

		if !isParticipant {
			// Check if it's a group
			isMember, groupErr := s.groupRepo.IsMember(ctx, targetChatID, userID)
			if groupErr != nil || !isMember {
				continue // Skip chats where user is not a participant
			}
			isGroup = true
		}

		// 3. Clone and send each message
		for _, origMsg := range originalMessages {
			// Determine original sender
			originalSenderID := origMsg.SenderID
			if origMsg.IsForwarded && origMsg.OriginalSenderID != nil {
				originalSenderID = *origMsg.OriginalSenderID
			}

			newMsg := &domain.Message{
				ID:               uuid.New(),
				ConversationID:   targetChatID,
				SenderID:         userID,
				Content:          origMsg.Content,
				ContentHash:      origMsg.ContentHash,
				EncryptedKeys:    origMsg.EncryptedKeys,
				Type:             origMsg.Type,
				AttachmentURL:    origMsg.AttachmentURL,
				AttachmentType:   origMsg.AttachmentType,
				AttachmentSize:   origMsg.AttachmentSize,
				Metadata:         origMsg.Metadata,
				IsForwarded:      true,
				OriginalSenderID: &originalSenderID,
				CreatedAt:        time.Now(),
				UpdatedAt:        time.Now(),
			}

			if err := s.messageRepo.Create(ctx, newMsg); err != nil {
				s.logger.Error("Failed to create forwarded message", zap.Error(err))
				continue
			}

			// Get full view for notification
			msgView, err := s.messageRepo.GetByID(ctx, newMsg.ID)
			if err != nil {
				s.logger.Error("Failed to get forwarded message view", zap.Error(err))
				continue
			}

			forwardedMessages = append(forwardedMessages, msgView)

			// 4. Notify participants
			var participants []int64
			if isGroup {
				participants, _ = s.groupRepo.GetGroupMembers(ctx, targetChatID)
				_ = s.groupRepo.UnhideGroup(ctx, targetChatID, userID) // Unhide for sender too
				for _, pID := range participants {
					_ = s.groupRepo.UnhideGroup(ctx, targetChatID, pID)
				}
			} else {
				participants, _ = s.chatRepo.GetConversationParticipants(ctx, targetChatID)
				for _, pID := range participants {
					_ = s.chatRepo.UnhideConversation(ctx, targetChatID, pID)
				}
			}
			recipientIDs := make([]int64, 0, len(participants))
			for _, participantID := range participants {
				if participantID != userID {
					recipientIDs = append(recipientIDs, participantID)
				}
			}
			if err := s.messageRepo.BulkCreateMessageStatus(ctx, newMsg.ID, recipientIDs); err != nil {
				s.logger.Warn("failed to create forwarded message statuses", zap.String("message_id", newMsg.ID.String()), zap.Error(err))
			}

			for _, participantID := range participants {
				if s.hub.IsUserOnline(participantID) {
					// Keep the sender's other devices in sync without waiting for a refetch.
					if participantID == userID {
						s.hub.SendMessageNotificationExceptSession(participantID, msgView, false, senderSessionKey)
					} else if s.hub.SendMessageNotification(participantID, msgView, false) > 0 {
						s.recordMessageDelivery(ctx, newMsg, participantID)
					}
				}

				if participantID == userID {
					continue
				}

				if !isGroup {
					_ = s.chatRepo.IncrementUnread(ctx, targetChatID, participantID)
				}

				muted := false
				if isGroup {
					muted, _ = s.groupRepo.IsMuted(ctx, targetChatID, participantID)
				} else {
					muted, _ = s.chatRepo.IsMuted(ctx, targetChatID, participantID)
				}
				if muted {
					continue
				}

				notificationLocale := s.notificationService.recipientLocale(ctx, participantID)
				title := i18n.SystemNotification(notificationLocale, "Новое сообщение")
				if msgView.SenderFullName != "" {
					title = msgView.SenderFullName
				}
				notificationBody := i18n.SystemNotification(notificationLocale, buildNotificationPreview(msgView.Type))
				if isGroup {
					notificationBody = i18n.SystemNotification(notificationLocale, "Пересланное сообщение в группе")
				}

				meta := map[string]string{
					"kind":            "chat_message",
					"conversation_id": targetChatID.String(),
					"message_id":      msgView.ID.String(),
					"sender_id":       fmt.Sprintf("%d", userID),
					"type":            msgView.Type,
					"is_forwarded":    "true",
				}

				s.notificationService.SendChatPushOnly(ctx, participantID, title, notificationBody, meta)
			}
		}
	}

	return forwardedMessages, nil
}

// ForwardPreparedMessages forwards messages whose content and message keys were
// re-encrypted by the client for the target chats.
func (s *ChatService) ForwardPreparedMessages(ctx context.Context, userID int64, inputs []ForwardMessageInput, senderSessionKey string) ([]*domain.MessageView, error) {
	if len(inputs) == 0 {
		return nil, apperrors.BadRequest("messages cannot be empty")
	}

	originalIDs := make([]uuid.UUID, 0, len(inputs))
	seenOriginalIDs := make(map[uuid.UUID]bool, len(inputs))
	for _, input := range inputs {
		if input.OriginalMessageID == uuid.Nil || input.TargetChatID == uuid.Nil {
			return nil, apperrors.BadRequest("original_message_id and target_chat_id are required")
		}
		if strings.TrimSpace(input.Content) == "" {
			return nil, apperrors.BadRequest("forwarded message content cannot be empty")
		}
		if !seenOriginalIDs[input.OriginalMessageID] {
			originalIDs = append(originalIDs, input.OriginalMessageID)
			seenOriginalIDs[input.OriginalMessageID] = true
		}
	}

	originalMessages, err := s.messageRepo.GetByIDs(ctx, originalIDs)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get original messages", err)
	}

	originalByID := make(map[uuid.UUID]*domain.MessageView, len(originalMessages))
	for _, message := range originalMessages {
		if err := s.ensureCanAccessMessage(ctx, userID, message); err != nil {
			return nil, err
		}
		originalByID[message.ID] = message
	}

	var forwardedMessages []*domain.MessageView
	for _, input := range inputs {
		origMsg := originalByID[input.OriginalMessageID]
		if origMsg == nil {
			return nil, apperrors.MessageNotFound()
		}

		msgView, err := s.createForwardedMessage(ctx, userID, input.TargetChatID, origMsg, input.Content, input.ContentHash, input.Keys, senderSessionKey)
		if err != nil {
			return nil, err
		}
		forwardedMessages = append(forwardedMessages, msgView)
	}

	return forwardedMessages, nil
}

func (s *ChatService) ensureCanAccessMessage(ctx context.Context, userID int64, message *domain.MessageView) error {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, message.ConversationID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to check original message participant", err)
	}
	if isParticipant {
		return nil
	}

	isMember, err := s.groupRepo.IsMember(ctx, message.ConversationID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to check original message group membership", err)
	}
	if !isMember {
		return apperrors.NotParticipant()
	}
	return nil
}

func (s *ChatService) createForwardedMessage(
	ctx context.Context,
	userID int64,
	targetChatID uuid.UUID,
	origMsg *domain.MessageView,
	content string,
	contentHash string,
	keys map[string]string,
	senderSessionKey string,
) (*domain.MessageView, error) {
	isGroup := false
	isParticipant, err := s.chatRepo.IsParticipant(ctx, targetChatID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant", err)
	}
	if !isParticipant {
		isMember, groupErr := s.groupRepo.IsMember(ctx, targetChatID, userID)
		if groupErr != nil {
			return nil, apperrors.InternalWithErr("failed to check group membership", groupErr)
		}
		if !isMember {
			return nil, apperrors.NotParticipant()
		}
		isGroup = true
	}

	originalSenderID := origMsg.SenderID
	if origMsg.IsForwarded && origMsg.OriginalSenderID != nil {
		originalSenderID = *origMsg.OriginalSenderID
	}

	newMsg := &domain.Message{
		ID:               uuid.New(),
		ConversationID:   targetChatID,
		SenderID:         userID,
		Content:          &content,
		ContentHash:      &contentHash,
		EncryptedKeys:    keys,
		Type:             origMsg.Type,
		AttachmentURL:    origMsg.AttachmentURL,
		AttachmentType:   origMsg.AttachmentType,
		AttachmentSize:   origMsg.AttachmentSize,
		Metadata:         origMsg.Metadata,
		IsForwarded:      true,
		OriginalSenderID: &originalSenderID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := s.messageRepo.Create(ctx, newMsg); err != nil {
		return nil, apperrors.InternalWithErr("failed to create forwarded message", err)
	}

	msgView, err := s.messageRepo.GetByID(ctx, newMsg.ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get forwarded message", err)
	}

	s.notifyForwardedMessage(ctx, userID, targetChatID, msgView, isGroup, senderSessionKey)
	return msgView, nil
}

func (s *ChatService) notifyForwardedMessage(ctx context.Context, userID int64, targetChatID uuid.UUID, msgView *domain.MessageView, isGroup bool, senderSessionKey string) {
	var participants []int64
	if isGroup {
		participants, _ = s.groupRepo.GetGroupMembers(ctx, targetChatID)
		_ = s.groupRepo.UnhideGroup(ctx, targetChatID, userID)
		for _, pID := range participants {
			_ = s.groupRepo.UnhideGroup(ctx, targetChatID, pID)
		}
	} else {
		participants, _ = s.chatRepo.GetConversationParticipants(ctx, targetChatID)
		for _, pID := range participants {
			_ = s.chatRepo.UnhideConversation(ctx, targetChatID, pID)
		}
	}
	recipientIDs := make([]int64, 0, len(participants))
	for _, participantID := range participants {
		if participantID != userID {
			recipientIDs = append(recipientIDs, participantID)
		}
	}
	if err := s.messageRepo.BulkCreateMessageStatus(ctx, msgView.ID, recipientIDs); err != nil {
		s.logger.Warn("failed to create forwarded message statuses", zap.String("message_id", msgView.ID.String()), zap.Error(err))
	}

	for _, participantID := range participants {
		if s.hub.IsUserOnline(participantID) {
			if participantID == userID {
				s.hub.SendMessageNotificationExceptSession(participantID, msgView, false, senderSessionKey)
			} else if s.hub.SendMessageNotification(participantID, msgView, false) > 0 {
				s.recordMessageDelivery(ctx, &msgView.Message, participantID)
			}
		}

		if participantID == userID {
			continue
		}

		if !isGroup {
			_ = s.chatRepo.IncrementUnread(ctx, targetChatID, participantID)
		}

		muted := false
		if isGroup {
			muted, _ = s.groupRepo.IsMuted(ctx, targetChatID, participantID)
		} else {
			muted, _ = s.chatRepo.IsMuted(ctx, targetChatID, participantID)
		}
		if muted {
			continue
		}

		notificationLocale := s.notificationService.recipientLocale(ctx, participantID)
		title := i18n.SystemNotification(notificationLocale, "Новое сообщение")
		if msgView.SenderFullName != "" {
			title = msgView.SenderFullName
		}
		notificationBody := i18n.SystemNotification(notificationLocale, buildNotificationPreview(msgView.Type))
		if isGroup {
			notificationBody = i18n.SystemNotification(notificationLocale, "Пересланное сообщение в группе")
		}

		meta := map[string]string{
			"kind":            "chat_message",
			"conversation_id": targetChatID.String(),
			"message_id":      msgView.ID.String(),
			"sender_id":       fmt.Sprintf("%d", userID),
			"type":            msgView.Type,
			"is_forwarded":    "true",
		}

		s.notificationService.SendChatPushOnly(ctx, participantID, title, notificationBody, meta)
	}
}

// SendMessage sends a message in conversation
func (s *ChatService) SendMessage(
	ctx context.Context,
	conversationID uuid.UUID,
	senderID int64,
	content string,
	contentHash string,
	keys map[string]string,
	messageType string,
	metadata *string,
	replyToID *uuid.UUID,
	isEphemeral bool,
	ttlSeconds *int,
	attachment *MessageAttachmentInput,
	senderSessionKey string,
) (*domain.MessageView, error) {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, senderID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant", err)
	}
	if !isParticipant {
		return nil, apperrors.NotParticipant()
	}

	attachmentURL, attachmentType, attachmentSize, err := normalizeMessageAttachment(messageType, attachment)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" && attachmentURL == nil {
		return nil, apperrors.BadRequest("message content cannot be empty")
	}
	if strings.TrimSpace(messageType) == "" {
		messageType = inferMessageType(attachmentType)
	}

	// Проверяем, что сообщение, на которое отвечают, существует и принадлежит этому же разговору
	if replyToID != nil {
		replyToMessage, err := s.messageRepo.GetByID(ctx, *replyToID)
		if err != nil {
			return nil, apperrors.BadRequest("replied message not found")
		}
		if replyToMessage.ConversationID != conversationID {
			return nil, apperrors.BadRequest("replied message does not belong to this conversation")
		}
	}

	participants, err := s.chatRepo.GetConversationParticipants(ctx, conversationID)
	if err == nil {
		for _, participantID := range participants {
			_ = s.chatRepo.UnhideConversation(ctx, conversationID, participantID)
		}
	}

	message := &domain.Message{
		ID:             uuid.New(),
		ConversationID: conversationID,
		SenderID:       senderID,
		ReplyToID:      replyToID,
		Content:        &content,
		ContentHash:    &contentHash,
		EncryptedKeys:  keys,
		Type:           messageType,
		AttachmentURL:  attachmentURL,
		AttachmentType: attachmentType,
		AttachmentSize: attachmentSize,
		AttachmentName: attachmentName(attachment),
		Metadata:       metadata,
		IsEdited:       false,
		IsDeleted:      false,
		IsEphemeral:    isEphemeral,
		TTLSeconds:     ttlSeconds,
	}

	if err := s.messageRepo.Create(ctx, message); err != nil {
		return nil, apperrors.InternalWithErr("failed to create message", err)
	}

	s.publishMessageEvent("chat.message.sent", message)

	messageView, err := s.messageRepo.GetByID(ctx, message.ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get message", err)
	}

	mentionedUserIDs := mentionedUserIDsFromMetadata(ctx, s.userRepo, metadata)
	if len(mentionedUserIDs) > 0 {
		participantSet := make(map[int64]bool, len(participants))
		for _, participantID := range participants {
			participantSet[participantID] = true
		}

		mentionIDs := make([]int64, 0, len(mentionedUserIDs))
		for mentionID := range mentionedUserIDs {
			if participantSet[mentionID] && mentionID != senderID {
				mentionIDs = append(mentionIDs, mentionID)
			} else {
				delete(mentionedUserIDs, mentionID)
			}
		}
		if len(mentionIDs) > 0 {
			_ = s.messageRepo.CreateMentions(ctx, message.ID, mentionIDs)
		}
	}

	nonSenderIDs := make([]int64, 0, len(participants))
	for _, pid := range participants {
		if pid != senderID {
			nonSenderIDs = append(nonSenderIDs, pid)
		}
	}
	if len(nonSenderIDs) > 0 {
		_ = s.messageRepo.BulkCreateMessageStatus(ctx, message.ID, nonSenderIDs)
	}

	for _, participantID := range participants {
		if s.hub.IsUserOnline(participantID) {
			if participantID == senderID {
				s.hub.SendMessageNotificationExceptSession(participantID, messageView, mentionedUserIDs[participantID], senderSessionKey)
			} else {
				if s.hub.SendMessageNotification(participantID, messageView, mentionedUserIDs[participantID]) > 0 {
					s.recordMessageDelivery(ctx, message, participantID)
				}
			}
		}

		if participantID == senderID {
			continue
		}

		_ = s.chatRepo.IncrementUnread(ctx, conversationID, participantID)

		muted, _ := s.chatRepo.IsMuted(ctx, conversationID, participantID)
		if muted && !mentionedUserIDs[participantID] {
			continue
		}

		meta := map[string]string{
			"kind":            "chat_message",
			"conversation_id": conversationID.String(),
			"message_id":      message.ID.String(),
			"sender_id":       fmt.Sprintf("%d", senderID),
			"type":            message.Type,
		}
		if mentionedUserIDs[participantID] {
			meta["is_mention"] = "true"
		}

		notificationLocale := s.notificationService.recipientLocale(ctx, participantID)
		title := i18n.SystemNotification(notificationLocale, "Новое сообщение")
		if messageView != nil && messageView.SenderFullName != "" {
			title = messageView.SenderFullName
		}
		if mentionedUserIDs[participantID] {
			title = i18n.SystemNotification(notificationLocale, "Вас упомянули")
		}

		notificationBody := i18n.SystemNotification(notificationLocale, buildNotificationPreview(messageType))
		if mentionedUserIDs[participantID] {
			notificationBody = i18n.SystemNotification(notificationLocale, "Вас упомянули в сообщении")
			if strings.TrimSpace(content) != "" && len([]rune(content)) <= 100 {
				notificationBody = content
			}
		}

		s.notificationService.SendChatPushOnly(ctx, participantID, title, notificationBody, meta)
	}

	s.logger.Info("Message sent",
		zap.String("message_id", message.ID.String()),
		zap.String("conversation_id", conversationID.String()),
		zap.Int64("sender_id", senderID),
	)

	// Гамификация: активность в чате — 1 балл раз в день
	if s.gamification != nil {
		go func() {
			// Файловые типы и обычные сообщения считаются одной "дневной" активностью.
			// Баллы начисляются не более 1 раза в сутки независимо от количества сообщений.
			switch messageType {
			case "file", "image", "audio", "video", "voice", "video_message":
				_ = s.gamification.TrackActivityOncePerDay(context.Background(), senderID, domain.ActionFileSentInChat)
			default:
				_ = s.gamification.TrackActivityOncePerDay(context.Background(), senderID, domain.ActionMessageSent)
			}
		}()
	}

	return messageView, nil
}

func (s *ChatService) MuteConversation(ctx context.Context, conversationID uuid.UUID, userID int64, mute bool) (*domain.ConversationView, error) {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to verify participant", err)
	}
	if !isParticipant {
		return nil, apperrors.NotParticipant()
	}

	if err := s.chatRepo.SetMute(ctx, conversationID, userID, mute); err != nil {
		return nil, apperrors.InternalWithErr("failed to update mute setting", err)
	}

	view, err := s.chatRepo.GetConversationView(ctx, conversationID, userID)
	if err == nil && view != nil && view.PeerID != nil && s.hub != nil && s.hub.IsUserOnline(*view.PeerID) {
		now := time.Now()
		view.PeerLastSeenAt = &now
	}
	return view, err
}

// buildNotificationPreview возвращает текст, который можно безопасно показывать в пуше/баннере,
func buildNotificationPreview(messageType string) string {
	switch messageType {
	case "text":
		return "Новое сообщение"
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

func mentionedUserIDsFromMetadata(ctx context.Context, userRepo *repository.UserRepository, metadata *string) map[int64]bool {
	result := make(map[int64]bool)
	if metadata == nil || strings.TrimSpace(*metadata) == "" || userRepo == nil {
		return result
	}

	var meta struct {
		Mentions []string `json:"mentions"`
	}
	if err := json.Unmarshal([]byte(*metadata), &meta); err != nil || len(meta.Mentions) == 0 {
		return result
	}

	resolved, err := userRepo.GetIDsByUsernames(ctx, meta.Mentions)
	if err != nil {
		return result
	}
	for _, id := range resolved {
		result[id] = true
	}
	return result
}

// GetMessages gets messages from conversation
func (s *ChatService) GetMessages(ctx context.Context, conversationID uuid.UUID, userID int64, currentDeviceID string, limit, offset int) ([]*domain.MessageView, error) {
	// Check if user is participant
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant", err)
	}
	if !isParticipant {
		return nil, apperrors.NotParticipant()
	}

	// Validate pagination
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	// Reset unread count when opening chat (getting messages)
	// We assume if user requests messages, they "read" the chat
	// This is a common pattern. Alternatively, explicit MarkAsRead call can be required.
	// For now, let's stick to explicit MarkAsRead for resetting specific message status,
	// BUT for the "badge" count on the home screen, fetching messages usually clears it in UI.
	// Let's keep strict logic: Only MarkAsRead clears.
	// OR: If offset=0 (first page), we can consider it as "opening chat".
	if offset == 0 {
		_ = s.chatRepo.ResetUnread(ctx, conversationID, userID)
	}

	// Получаем все сообщения
	messages, err := s.messageRepo.GetConversationMessages(ctx, conversationID, userID, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get messages", err)
	}

	// ВАЖНО: Фильтруем сообщения по last_cleared_at
	// Показываем только сообщения после того времени, когда пользователь скрыл чат
	// ВАЖНО: Фильтруем сообщения по last_cleared_at
	// Показываем только сообщения после того времени, когда пользователь скрыл чат
	lastClearedAt, _ := s.chatRepo.GetLastClearedAt(ctx, conversationID, userID)

	var processedMessages []*domain.MessageView
	if lastClearedAt != nil {
		for _, msg := range messages {
			if msg.CreatedAt.After(*lastClearedAt) {
				processedMessages = append(processedMessages, msg)
			}
		}
	} else {
		processedMessages = messages
	}

	s.aliasRestoredIdentityHistory(ctx, userID, currentDeviceID, processedMessages)

	// Enrich with reactions
	if len(processedMessages) > 0 {
		msgIDs := make([]uuid.UUID, len(processedMessages))
		for i, m := range processedMessages {
			msgIDs[i] = m.ID
		}

		reactionsMap, err := s.reactionRepo.GetReactionsByMessageIDs(ctx, msgIDs)
		if err == nil {
			for _, m := range processedMessages {
				if reacts, ok := reactionsMap[m.ID]; ok {
					m.Reactions = s.buildReactionSummaries(reacts, userID)
				}
			}
		} else {
			s.logger.Warn("Failed to fetch reactions", zap.Error(err))
		}
	}

	return processedMessages, nil
}

// GetMessageThread returns all visible descendants of a message as a nested
// tree. The existing reply_to payload remains unchanged for list endpoints.
func (s *ChatService) GetMessageThread(ctx context.Context, rootID uuid.UUID, userID int64, limit int) (*domain.MessageThreadResponse, error) {
	root, err := s.messageRepo.GetByID(ctx, rootID)
	if err != nil {
		return nil, apperrors.MessageNotFound()
	}
	if err := s.ensureCanAccessMessage(ctx, userID, root); err != nil {
		return nil, err
	}
	visible, err := s.messageRepo.IsVisibleToUser(ctx, rootID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check message visibility", err)
	}
	if !visible {
		return nil, apperrors.MessageNotFound()
	}

	if limit <= 0 || limit > maxReplyTreeMessages {
		limit = maxReplyTreeMessages
	}
	descendants, truncated, err := s.messageRepo.GetReplyDescendants(ctx, rootID, userID, limit)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get message replies", err)
	}

	allMessages := make([]*domain.MessageView, 0, len(descendants)+1)
	allMessages = append(allMessages, root)
	allMessages = append(allMessages, descendants...)
	// GetByID/GetByIDs attach quotes without viewer-specific visibility checks.
	// Never expose an older/cleared/deleted quote through a visible thread.
	for _, message := range allMessages {
		if message.ReplyTo == nil {
			continue
		}
		quoteVisible, err := s.messageRepo.IsVisibleToUser(ctx, message.ReplyTo.ID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to check reply visibility", err)
		}
		if !quoteVisible || message.ReplyTo.ConversationID != root.ConversationID {
			message.ReplyTo = nil
		}
	}
	s.enrichMessageReactions(ctx, allMessages, userID)

	return buildMessageThread(root, descendants, truncated), nil
}

func (s *ChatService) enrichMessageReactions(ctx context.Context, messages []*domain.MessageView, userID int64) {
	if len(messages) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			ids = append(ids, message.ID)
		}
	}
	reactionsByMessage, err := s.reactionRepo.GetReactionsByMessageIDs(ctx, ids)
	if err != nil {
		s.logger.Warn("Failed to enrich thread reactions", zap.Error(err))
		return
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		message.Reactions = s.buildReactionSummaries(reactionsByMessage[message.ID], userID)
	}
}

func buildMessageThread(root *domain.MessageView, descendants []*domain.MessageView, truncated bool) *domain.MessageThreadResponse {
	sort.SliceStable(descendants, func(i, j int) bool {
		if descendants[i].CreatedAt.Equal(descendants[j].CreatedAt) {
			return descendants[i].ID.String() < descendants[j].ID.String()
		}
		return descendants[i].CreatedAt.Before(descendants[j].CreatedAt)
	})

	nodes := make(map[uuid.UUID]*domain.MessageReplyNode, len(descendants))
	for _, message := range descendants {
		if message == nil || message.ConversationID != root.ConversationID {
			continue
		}
		nodes[message.ID] = &domain.MessageReplyNode{
			Message: message,
			Replies: make([]*domain.MessageReplyNode, 0),
		}
	}

	topLevel := make([]*domain.MessageReplyNode, 0)
	for _, message := range descendants {
		node := nodes[message.ID]
		if node == nil || message.ReplyToID == nil {
			continue
		}
		if *message.ReplyToID == root.ID {
			topLevel = append(topLevel, node)
			continue
		}
		if parent := nodes[*message.ReplyToID]; parent != nil {
			parent.Replies = append(parent.Replies, node)
		}
	}

	return &domain.MessageThreadResponse{
		Root:       root,
		Replies:    topLevel,
		ReplyCount: len(nodes),
		Truncated:  truncated,
	}
}

// GetSharedMedia returns paginated image/video attachments for a conversation.
func (s *ChatService) GetSharedMedia(ctx context.Context, conversationID uuid.UUID, userID int64, limit, offset int) (*domain.SharedMediaResponse, error) {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant", err)
	}
	if !isParticipant {
		return nil, apperrors.NotParticipant()
	}

	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}

	lastClearedAt, err := s.chatRepo.GetLastClearedAt(ctx, conversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get conversation clear state", err)
	}

	items, total, err := s.messageRepo.GetConversationSharedMedia(ctx, conversationID, userID, limit, offset, lastClearedAt)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get shared media", err)
	}

	return &domain.SharedMediaResponse{
		Items:   items,
		Total:   total,
		HasMore: offset+len(items) < total,
	}, nil
}

func (s *ChatService) aliasRestoredIdentityHistory(ctx context.Context, userID int64, currentDeviceID string, messages []*domain.MessageView) {
	currentDeviceID = strings.TrimSpace(currentDeviceID)
	if currentDeviceID == "" || len(messages) == 0 {
		return
	}

	equivalentDeviceIDs, err := s.messageRepo.GetEquivalentDeviceIDs(ctx, userID, currentDeviceID)
	if err != nil {
		s.logger.Warn("failed to resolve equivalent device ids for restored identity history access",
			zap.Int64("user_id", userID),
			zap.String("device_id", currentDeviceID),
			zap.Error(err),
		)
		return
	}
	if len(equivalentDeviceIDs) == 0 {
		return
	}

	aliasEncryptedKeysForDevice(messages, currentDeviceID, equivalentDeviceIDs)
}

// EditMessage edits a message
func (s *ChatService) EditMessage(ctx context.Context, messageID uuid.UUID, userID int64, newContent string) error {
	// Get message
	messageView, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return apperrors.MessageNotFound()
	}

	participants, _, err := s.messageAudience(ctx, messageView.ConversationID, userID)
	if err != nil {
		return err
	}
	// Check after membership to avoid exposing message existence to outsiders.
	if messageView.SenderID != userID {
		return apperrors.CannotEditMessage()
	}
	if !isWithinModerationWindow(messageView.CreatedAt, time.Now(), MessageEditWindow) {
		return apperrors.EditWindowExpired()
	}

	// Update message
	messageView.Content = &newContent
	if err := s.messageRepo.Update(ctx, &messageView.Message); err != nil {
		return apperrors.InternalWithErr("failed to update message", err)
	}

	// Get updated message view with all details
	updatedMessageView, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		s.logger.Error("Failed to get updated message for notification", zap.Error(err))
	} else {
		// Send WebSocket notification to all conversation participants
		for _, participantID := range participants {
			// Send to all members including sender (so all their devices get the update).
			if s.hub.IsUserOnline(participantID) {
				s.hub.SendMessageEditNotification(participantID, updatedMessageView)
			}
		}
	}

	s.logger.Info("Message edited",
		zap.String("message_id", messageID.String()),
		zap.Int64("user_id", userID),
	)

	publishEventAsync(s.logger, s.eventPublisher, "chat.message.edited", chatMessageEditPayload{
		MessageID:      messageID,
		ConversationID: messageView.ConversationID,
		EditedBy:       userID,
	}, zap.String("message_id", messageID.String()))

	return nil
}

// DeleteMessage deletes a message for the caller or, when requested and
// allowed by moderation rules, for every conversation participant.
func (s *ChatService) DeleteMessage(ctx context.Context, messageID uuid.UUID, userID int64, scope MessageDeleteScope) error {
	// Get message to verify it exists and get conversation ID
	msg, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return apperrors.MessageNotFound()
	}

	participants, isGroup, err := s.messageAudience(ctx, msg.ConversationID, userID)
	if err != nil {
		return err
	}

	if scope == MessageDeleteForEveryone {
		if err := s.canDeleteForEveryone(ctx, msg, userID, isGroup); err != nil {
			return err
		}
		if err := s.messageRepo.Delete(ctx, messageID); err != nil {
			return apperrors.InternalWithErr("failed to delete message for everyone", err)
		}
		if err := s.pinnedRepo.UnpinMessage(ctx, messageID); err != nil {
			s.logger.Warn("failed to unpin deleted message", zap.String("message_id", messageID.String()), zap.Error(err))
		}
	} else {
		if err := s.messageRepo.DeleteMessagesForUser(ctx, userID, []uuid.UUID{messageID}); err != nil {
			return apperrors.InternalWithErr("failed to delete message", err)
		}
	}

	// Fetch the updated new last message for the chat list
	var newLastMessage interface{}
	convView, err := s.chatRepo.GetConversationView(ctx, msg.ConversationID, userID)
	if err == nil {
		if convView.LastMessage != nil {
			newLastMessage = convView.LastMessage
		}
	} else {
		// Try as group
		groupView, err := s.groupRepo.GetGroupView(ctx, msg.ConversationID, userID)
		if err == nil && groupView.LastMessage != nil {
			newLastMessage = groupView.LastMessage
		}
	}

	notifyUsers := []int64{userID}
	if scope == MessageDeleteForEveryone {
		notifyUsers = participants
	}
	for _, notifyUserID := range notifyUsers {
		if s.hub.IsUserOnline(notifyUserID) {
			s.hub.SendMessageDeleteNotification(notifyUserID, msg.ConversationID, messageID, scope == MessageDeleteForEveryone, newLastMessage)
		}
	}

	s.logger.Info("Message deleted for user",
		zap.String("message_id", messageID.String()),
		zap.Int64("user_id", userID),
	)

	routingKey := "chat.message.deleted_for_user"
	if scope == MessageDeleteForEveryone {
		routingKey = "chat.message.deleted_for_everyone"
	}
	publishEventAsync(s.logger, s.eventPublisher, routingKey, chatMessageDeletePayload{
		MessageID:      messageID,
		ConversationID: msg.ConversationID,
		UserID:         userID,
	}, zap.String("message_id", messageID.String()), zap.Int64("user_id", userID))

	return nil
}

// HideConversation скрывает conversation для пользователя
// Любой участник может скрыть чат у себя, это не повлияет на других
func (s *ChatService) HideConversation(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to verify participant", err)
	}
	if !isParticipant {
		return apperrors.NotParticipant()
	}

	participants, err := s.chatRepo.GetConversationParticipants(ctx, conversationID)
	if err != nil {
		return apperrors.InternalWithErr("failed to get conversation participants", err)
	}

	isSupportChat := false
	for _, pID := range participants {
		if pID == domain.SupportUserID {
			isSupportChat = true
			break
		}
	}

	if isSupportChat && userID != domain.SupportUserID {
		return apperrors.BadRequest("Этот чат является системным (Техподдержка) и не может быть скрыт.")
	}

	if err := s.chatRepo.HideConversation(ctx, conversationID, userID); err != nil {
		return apperrors.InternalWithErr("failed to hide conversation", err)
	}

	// Notify all devices of this user that the chat was hidden/cleared
	if s.hub.IsUserOnline(userID) {
		s.hub.SendChatHiddenNotification(userID, conversationID, "personal")
	}

	// Unpin chat if it was pinned
	if err := s.pinnedRepo.UnpinChat(ctx, userID, conversationID, "personal"); err != nil {
		s.logger.Warn("Failed to unpin hidden conversation",
			zap.String("conversation_id", conversationID.String()),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	s.logger.Info("Conversation hidden by user",
		zap.String("conversation_id", conversationID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

// UnhideConversation восстанавливает скрытый conversation для пользователя
func (s *ChatService) UnhideConversation(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	// Verify user is participant
	isParticipant, err := s.chatRepo.IsParticipant(ctx, conversationID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to verify participant", err)
	}
	if !isParticipant {
		return apperrors.NotParticipant()
	}

	// Unhide conversation
	if err := s.chatRepo.UnhideConversation(ctx, conversationID, userID); err != nil {
		return apperrors.InternalWithErr("failed to unhide conversation", err)
	}

	s.logger.Info("Conversation unhidden by user",
		zap.String("conversation_id", conversationID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

// MarkAsRead marks message as read
func (s *ChatService) MarkAsRead(ctx context.Context, messageID uuid.UUID, userID int64) error {
	// Get message to find conversation ID
	messageView, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return apperrors.MessageNotFound()
	}
	if _, _, err := s.messageAudience(ctx, messageView.ConversationID, userID); err != nil {
		return err
	}

	// Mark all messages up to this one as read
	updates, err := s.messageRepo.MarkMessagesAsRead(ctx, messageView.ConversationID, userID, messageID)
	if err != nil {
		return apperrors.InternalWithErr("failed to mark messages as read", err)
	}

	for _, update := range updates {
		_, readCount, countErr := s.messageRepo.GetStatusCounts(ctx, update.MessageID)
		if countErr != nil {
			s.logger.Warn("failed to load read count", zap.String("message_id", update.MessageID.String()), zap.Error(countErr))
			continue
		}
		s.hub.SendReadReceipt(update.SenderID, userID, update.MessageID, messageView.ConversationID, readCount)
	}

	unreadCount, err := s.messageRepo.CountUnreadMessages(ctx, messageView.ConversationID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to count unread messages", err)
	}
	if err := s.chatRepo.SetUnreadCount(ctx, messageView.ConversationID, userID, unreadCount); err != nil {
		s.logger.Warn("failed to synchronize unread count", zap.Error(err))
	}
	s.hub.SendUnreadCountUpdated(userID, messageView.ConversationID, unreadCount)

	return nil
}

// MarkMessageAsDelivered marks a message as delivered for the given user and notifies the sender.
func (s *ChatService) MarkMessageAsDelivered(ctx context.Context, messageID uuid.UUID, userID int64) error {
	messageView, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return apperrors.MessageNotFound()
	}
	if _, _, err := s.messageAudience(ctx, messageView.ConversationID, userID); err != nil {
		return err
	}
	if messageView.SenderID == userID {
		return apperrors.Forbidden("message sender cannot mark their own message as delivered")
	}
	changed, err := s.messageRepo.MarkAsDelivered(ctx, messageID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to mark as delivered", err)
	}
	if changed {
		deliveredCount, _, countErr := s.messageRepo.GetStatusCounts(ctx, messageID)
		if countErr != nil {
			return apperrors.InternalWithErr("failed to load delivery count", countErr)
		}
		s.hub.SendDeliveryReceipt(messageView.SenderID, userID, messageID, messageView.ConversationID, deliveredCount)
	}
	return nil
}

func (s *ChatService) recordMessageDelivery(ctx context.Context, message *domain.Message, recipientID int64) {
	changed, err := s.messageRepo.MarkAsDelivered(ctx, message.ID, recipientID)
	if err != nil {
		s.logger.Warn("failed to mark websocket delivery", zap.String("message_id", message.ID.String()), zap.Int64("recipient_id", recipientID), zap.Error(err))
		return
	}
	if !changed {
		return
	}
	deliveredCount, _, err := s.messageRepo.GetStatusCounts(ctx, message.ID)
	if err != nil {
		s.logger.Warn("failed to load delivery count", zap.String("message_id", message.ID.String()), zap.Error(err))
		return
	}
	s.hub.SendDeliveryReceipt(message.SenderID, recipientID, message.ID, message.ConversationID, deliveredCount)
}

// BroadcastUserStatus notifies all connected users about status change
func (s *ChatService) BroadcastUserStatus(_ context.Context, userID int64, status string) {
	msg := &websocket.Message{
		Type: "user_status",
		From: userID,
		Content: map[string]interface{}{
			"user_id":      userID,
			"status":       status,
			"last_seen_at": time.Now(),
		},
		Timestamp: time.Now().Unix(),
	}

	s.hub.Broadcast(msg)
}

// DeleteMessages deletes multiple messages for the current user (Delete for Me)
func (s *ChatService) DeleteMessages(ctx context.Context, messageIDs []uuid.UUID, userID int64, scope MessageDeleteScope) error {
	// Get messages to check existence and probably participation
	msgs, err := s.messageRepo.GetByIDs(ctx, messageIDs)
	if err != nil {
		return apperrors.InternalWithErr("failed to get messages", err)
	}

	if len(msgs) != len(messageIDs) {
		return apperrors.MessageNotFound()
	}

	audiences := make(map[uuid.UUID][]int64)
	for _, msg := range msgs {
		participants, isGroup, accessErr := s.messageAudience(ctx, msg.ConversationID, userID)
		if accessErr != nil {
			return accessErr
		}
		audiences[msg.ConversationID] = participants
		if scope == MessageDeleteForEveryone {
			if err := s.canDeleteForEveryone(ctx, msg, userID, isGroup); err != nil {
				return err
			}
		}
	}

	if scope == MessageDeleteForEveryone {
		if err := s.messageRepo.DeleteMessages(ctx, messageIDs); err != nil {
			return apperrors.InternalWithErr("failed to delete messages for everyone", err)
		}
		for _, messageID := range messageIDs {
			if err := s.pinnedRepo.UnpinMessage(ctx, messageID); err != nil {
				s.logger.Warn("failed to unpin deleted message", zap.String("message_id", messageID.String()), zap.Error(err))
			}
		}
	} else if err := s.messageRepo.DeleteMessagesForUser(ctx, userID, messageIDs); err != nil {
		return apperrors.InternalWithErr("failed to delete messages", err)
	}

	// We take only one message to get the ConversationID (assuming all are from the same chat for notifications simplicity,
	// or we notify for each if they are from different chats)
	// For simplicity, let's group by conversation if they are different.
	convMessages := make(map[uuid.UUID]uuid.UUID) // convID -> lastDeletedMsgID
	for _, m := range msgs {
		convMessages[m.ConversationID] = m.ID
	}

	// Notify all devices of this user for each affected conversation
	for convID, lastDeletedID := range convMessages {
		notifyUsers := []int64{userID}
		if scope == MessageDeleteForEveryone {
			notifyUsers = audiences[convID]
		}
		for _, notifyUserID := range notifyUsers {
			if !s.hub.IsUserOnline(notifyUserID) {
				continue
			}
			var newLastMessage interface{}
			convView, err := s.chatRepo.GetConversationView(ctx, convID, notifyUserID)
			if err == nil {
				if convView.LastMessage != nil {
					newLastMessage = convView.LastMessage
				}
			} else {
				groupView, err := s.groupRepo.GetGroupView(ctx, convID, notifyUserID)
				if err == nil && groupView.LastMessage != nil {
					newLastMessage = groupView.LastMessage
				}
			}
			s.hub.SendMessageDeleteNotification(notifyUserID, convID, lastDeletedID, scope == MessageDeleteForEveryone, newLastMessage)
		}
	}

	s.logger.Info("Messages deleted for user",
		zap.Int("count", len(messageIDs)),
		zap.Int64("user_id", userID),
	)

	routingKey := "chat.message.bulk_deleted_for_user"
	if scope == MessageDeleteForEveryone {
		routingKey = "chat.message.bulk_deleted_for_everyone"
	}
	publishEventAsync(s.logger, s.eventPublisher, routingKey, chatBulkDeletePayload{
		MessageIDs: append([]uuid.UUID(nil), messageIDs...),
		UserID:     userID,
	}, zap.Int64("user_id", userID), zap.Int("count", len(messageIDs)))

	return nil
}

// GetMessageViewers returns a list of users who viewed (read) the message
func (s *ChatService) GetMessageViewers(ctx context.Context, userID int64, messageID uuid.UUID) ([]domain.MessageViewer, error) {
	// 1. Get message to find its conversation
	message, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return nil, apperrors.MessageNotFound()
	}

	// 2. Permission Check: requestor must be a participant of the conversation or group
	isParticipant, err := s.chatRepo.IsParticipant(ctx, message.ConversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant status", err)
	}

	if !isParticipant {
		// Also check group membership
		isMember, groupErr := s.groupRepo.IsMember(ctx, message.ConversationID, userID)
		if groupErr != nil || !isMember {
			return nil, apperrors.Forbidden("you are not a participant of this conversation")
		}
	}

	// 3. Strict Requirement: Only the sender can see who read their message
	if message.SenderID != userID {
		return nil, apperrors.Forbidden("Только отправитель может видеть список прочитавших его сообщение")
	}

	return s.messageRepo.GetMessageViewers(ctx, messageID)
}

// AddReaction adds a reaction to a message
func (s *ChatService) AddReaction(ctx context.Context, messageID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	if reaction == "" || len([]rune(reaction)) > 32 {
		return nil, apperrors.BadRequest("reaction must contain between 1 and 32 characters")
	}

	// Check if message exists and user has access
	message, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return nil, apperrors.MessageNotFound()
	}

	// Check participation
	isParticipant, err := s.chatRepo.IsParticipant(ctx, message.ConversationID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check participant", err)
	}
	if !isParticipant {
		// Check group
		isMember, groupErr := s.groupRepo.IsMember(ctx, message.ConversationID, userID)
		if groupErr != nil || !isMember {
			return nil, apperrors.NotParticipant()
		}
	}

	reactionModel := &domain.MessageReaction{
		MessageID: messageID,
		UserID:    userID,
		Reaction:  reaction,
	}

	if err := s.reactionRepo.AddReaction(ctx, reactionModel); err != nil {
		return nil, apperrors.InternalWithErr("failed to add reaction", err)
	}

	publishEventAsync(s.logger, s.eventPublisher, "chat.reaction.added", chatReactionPayload{
		MessageID:      messageID,
		ConversationID: message.ConversationID,
		UserID:         userID,
		Reaction:       reaction,
	}, zap.String("message_id", messageID.String()), zap.Int64("user_id", userID))

	// Notify participants via WebSocket
	s.notifyReactionUpdate(ctx, message.ConversationID, messageID)

	// Fetch updated reactions for API response
	reactionsMap, err := s.reactionRepo.GetReactionsByMessageIDs(ctx, []uuid.UUID{messageID})
	if err != nil {
		s.logger.Error("Failed to fetch reactions for return", zap.Error(err))
		return []domain.ReactionSummary{}, nil
	}

	return s.buildReactionSummaries(reactionsMap[messageID], userID), nil
}

// RemoveReaction removes a reaction from a message
func (s *ChatService) RemoveReaction(ctx context.Context, messageID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	if reaction == "" || len([]rune(reaction)) > 32 {
		return nil, apperrors.BadRequest("reaction must contain between 1 and 32 characters")
	}

	message, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return nil, apperrors.MessageNotFound()
	}
	if err := s.ensureCanAccessMessage(ctx, userID, message); err != nil {
		return nil, err
	}

	if err := s.reactionRepo.RemoveReaction(ctx, messageID, userID, reaction); err != nil {
		return nil, apperrors.InternalWithErr("failed to remove reaction", err)
	}

	publishEventAsync(s.logger, s.eventPublisher, "chat.reaction.removed", chatReactionPayload{
		MessageID:      messageID,
		ConversationID: message.ConversationID,
		UserID:         userID,
		Reaction:       reaction,
	}, zap.String("message_id", messageID.String()), zap.Int64("user_id", userID))
	s.notifyReactionUpdate(ctx, message.ConversationID, messageID)

	// Fetch updated reactions for API response
	reactionsMap, err := s.reactionRepo.GetReactionsByMessageIDs(ctx, []uuid.UUID{messageID})
	if err != nil {
		s.logger.Error("Failed to fetch reactions for return", zap.Error(err))
		return []domain.ReactionSummary{}, nil
	}

	return s.buildReactionSummaries(reactionsMap[messageID], userID), nil
}

func (s *ChatService) notifyReactionUpdate(ctx context.Context, conversationID, messageID uuid.UUID) {
	// Fetch updated reactions
	reactionsMap, err := s.reactionRepo.GetReactionsByMessageIDs(ctx, []uuid.UUID{messageID})
	if err != nil {
		s.logger.Error("Failed to fetch updated reactions", zap.Error(err))
		return
	}

	reactions := reactionsMap[messageID]
	summaries := s.buildReactionSummaries(reactions, 0) // 0 means no "reacted" flag specific to one user for broadcast
	// We need to send the full updated message or just reactions update.
	// Ideally we send an event "reaction_updated" with message_id and new reactions list.
	// But `buildReactionSummaries` depends on `currentUserID` to set `Reacted: true`.
	// Since this is a broadcast, we can't easily set `Reacted` for everyone.
	// We might need to send the raw reactions or let the client handle it, OR send the event and client refetches or client updates local state.
	// Better: Send "reaction_added" / "reaction_removed" event?
	// Or send the list of reaction counts and list of user IDs?
	// Let's send a specific event `message_reactions_updated` with the content.

	// For simplicity in broadcast, we won't set `Reacted` in the payload passed to everyone.
	// The frontend should know if *they* reacted based on their own action, or we send the list of users who reacted.
	// Standard approach: Send `reactions: [...]` where `user_names` or `user_ids` are included.

	payload := map[string]interface{}{
		"message_id": messageID,
		"reactions":  summaries,
	}

	msg := &websocket.Message{
		Type:           "message_reactions_updated",
		ConversationID: &conversationID,
		MessageID:      &messageID,
		Content:        payload,
		Timestamp:      time.Now().Unix(),
	}

	// Broadcast to conversation participants
	participants, err := s.chatRepo.GetConversationParticipants(ctx, conversationID)
	if err == nil && len(participants) > 0 {
		for _, pID := range participants {
			if s.hub.IsUserOnline(pID) {
				userPayload := make(map[string]interface{})
				for k, v := range payload {
					userPayload[k] = v
				}
				userPayload["reactions"] = s.buildReactionSummaries(reactions, pID)

				userMsg := *msg // shallow copy
				userMsg.Content = userPayload
				s.hub.SendToUser(pID, &userMsg)
			}
		}
	} else {
		// Try group (either error OR empty participants list — this ID belongs to a group)
		members, err := s.groupRepo.GetGroupMembers(ctx, conversationID)
		if err == nil {
			for _, pID := range members {
				if s.hub.IsUserOnline(pID) {
					userPayload := make(map[string]interface{})
					for k, v := range payload {
						userPayload[k] = v
					}
					userPayload["reactions"] = s.buildReactionSummaries(reactions, pID)

					userMsg := *msg
					userMsg.Content = userPayload
					s.hub.SendToUser(pID, &userMsg)
				}
			}
		}
	}
}

func (s *ChatService) buildReactionSummaries(reactions []domain.MessageReaction, currentUserID int64) []domain.ReactionSummary {
	summaryMap := make(map[string]*domain.ReactionSummary)

	for _, r := range reactions {
		if _, exists := summaryMap[r.Reaction]; !exists {
			summaryMap[r.Reaction] = &domain.ReactionSummary{
				Reaction: r.Reaction,
				Count:    0,
				Reacted:  false,
			}
		}
		summary := summaryMap[r.Reaction]
		summary.Count++
		if r.UserID == currentUserID {
			summary.Reacted = true
		}
		if len(summary.Users) < 50 && r.UserFullName != "" {
			summary.Users = append(summary.Users, domain.ReactionUser{
				ID:       r.UserID,
				FullName: r.UserFullName,
				Avatar:   r.UserAvatar,
			})
		}
	}

	result := make([]domain.ReactionSummary, 0, len(summaryMap))
	for _, s := range summaryMap {
		result = append(result, *s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Reaction < result[j].Reaction
	})
	return result
}

// StartEphemeralCleaner starts a background worker to delete expired ephemeral messages
func (s *ChatService) StartEphemeralCleaner(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Ephemeral cleaner stopping over context done")
			return
		case <-ticker.C:
			s.cleanExpiredMessages()
		}
	}
}

func (s *ChatService) cleanExpiredMessages() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	expiredItems, err := s.messageRepo.DeleteExpiredMessages(ctx)
	if err != nil {
		s.logger.Error("Failed to run ephemeral cleaner cycle", zap.Error(err))
		return
	}

	if len(expiredItems) == 0 {
		return
	}

	s.logger.Info("Ephemeral messages deleted", zap.Int("count", len(expiredItems)))

	// Broadcast deletions (to everyone in that conversation who is online)
	type convCount struct {
		participants []int64
		deletedIDs   []uuid.UUID
	}
	summary := make(map[uuid.UUID]*convCount)

	for _, item := range expiredItems {
		if _, ok := summary[item.ConversationID]; !ok {
			// Find participants. Check chatRepo, then groupRepo.
			participants, err := s.chatRepo.GetConversationParticipants(ctx, item.ConversationID)
			if err != nil || len(participants) == 0 {
				participants, _ = s.groupRepo.GetGroupMembers(ctx, item.ConversationID)
			}
			summary[item.ConversationID] = &convCount{
				participants: participants,
			}
		}
		summary[item.ConversationID].deletedIDs = append(summary[item.ConversationID].deletedIDs, item.ID)
	}

	// For each conversation, notify all its participants for each deleted message
	for convID, info := range summary {
		for _, userID := range info.participants {
			if s.hub.IsUserOnline(userID) {
				var newLastMessage interface{}
				convView, err := s.chatRepo.GetConversationView(ctx, convID, userID)
				if err == nil {
					if convView.LastMessage != nil {
						newLastMessage = convView.LastMessage
					}
				} else {
					groupView, err := s.groupRepo.GetGroupView(ctx, convID, userID)
					if err == nil && groupView.LastMessage != nil {
						newLastMessage = groupView.LastMessage
					}
				}

				for _, msgID := range info.deletedIDs {
					s.hub.SendMessageDeleteNotification(userID, convID, msgID, false, newLastMessage)
				}
			}
		}
	}
}

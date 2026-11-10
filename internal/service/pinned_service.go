package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

const MaxPinnedChats = 5

type PinnedService struct {
	pinnedRepo  *repository.PinnedRepository
	chatRepo    *repository.ChatRepository
	groupRepo   *repository.GroupRepository
	channelRepo *repository.ChannelRepository
	hub         *websocket.Hub
	logger      *zap.Logger
}

func NewPinnedService(
	pinnedRepo *repository.PinnedRepository,
	chatRepo *repository.ChatRepository,
	groupRepo *repository.GroupRepository,
	channelRepo *repository.ChannelRepository,
	hub *websocket.Hub,
	logger *zap.Logger,
) *PinnedService {
	return &PinnedService{
		pinnedRepo:  pinnedRepo,
		chatRepo:    chatRepo,
		groupRepo:   groupRepo,
		channelRepo: channelRepo,
		hub:         hub,
		logger:      logger.With(zap.String("service", "pinned")),
	}
}

// PinChat pins a chat for user
func (s *PinnedService) PinChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string) error {
	// Check max pinned limit
	count, err := s.pinnedRepo.GetPinnedCount(ctx, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to get pinned count", err)
	}

	if count >= MaxPinnedChats {
		return apperrors.MaxPinnedChatsReached()
	}

	// Verify chat access based on type
	switch chatType {
	case "personal":
		isParticipant, err := s.chatRepo.IsParticipant(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check participant", err)
		}
		if !isParticipant {
			return apperrors.NotParticipant()
		}
	case "group":
		isMember, err := s.groupRepo.IsMember(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check member", err)
		}
		if !isMember {
			return apperrors.NotGroupMember()
		}
	case "channel":
		isSubscribed, err := s.channelRepo.IsSubscribed(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check channel subscription", err)
		}
		if !isSubscribed {
			return apperrors.NotSubscribed()
		}
	default:
		return apperrors.BadRequest("invalid chat type")
	}

	// Pin chat
	if err := s.pinnedRepo.PinChat(ctx, userID, chatID, chatType); err != nil {
		return apperrors.InternalWithErr("failed to pin chat", err)
	}

	s.logger.Info("Chat pinned",
		zap.Int64("user_id", userID),
		zap.String("chat_id", chatID.String()),
		zap.String("type", chatType),
	)

	return nil
}

// UnpinChat unpins a chat for user
func (s *PinnedService) UnpinChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string) error {
	if err := s.pinnedRepo.UnpinChat(ctx, userID, chatID, chatType); err != nil {
		return apperrors.InternalWithErr("failed to unpin chat", err)
	}

	s.logger.Info("Chat unpinned",
		zap.Int64("user_id", userID),
		zap.String("chat_id", chatID.String()),
	)

	return nil
}

// GetPinnedChats gets all pinned chats for user
func (s *PinnedService) GetPinnedChats(ctx context.Context, userID int64) (interface{}, error) {
	pinnedChats, err := s.pinnedRepo.GetPinnedChats(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get pinned chats", err)
	}

	return pinnedChats, nil
}

// PinMessage pins a message in chat
func (s *PinnedService) PinMessage(ctx context.Context, chatID uuid.UUID, chatType string, messageID uuid.UUID, userID int64) error {
	// Verify user has access to chat
	switch chatType {
	case "personal":
		isParticipant, err := s.chatRepo.IsParticipant(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check participant", err)
		}
		if !isParticipant {
			return apperrors.NotParticipant()
		}
	case "group":
		// Any member can pin in groups
		isMember, err := s.groupRepo.IsMember(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check member", err)
		}
		if !isMember {
			return apperrors.NotGroupMember()
		}
	case "channel":
		role, err := s.channelRepo.GetMemberRole(ctx, chatID, userID)
		if err != nil {
			return apperrors.NotParticipant()
		}
		if role != "owner" && role != "admin" {
			return apperrors.Forbidden("Only channel admins can pin messages")
		}
	default:
		return apperrors.BadRequest("invalid chat type")
	}
	belongs, err := s.pinnedRepo.MessageBelongsToChat(ctx, chatID, chatType, messageID)
	if err != nil {
		return apperrors.InternalWithErr("failed to validate pinned message", err)
	}
	if !belongs {
		return apperrors.BadRequest("message does not belong to the specified chat")
	}

	// Pin message
	if err := s.pinnedRepo.PinMessage(ctx, chatID, chatType, messageID, userID); err != nil {
		return apperrors.InternalWithErr("failed to pin message", err)
	}

	// For channels, also update the is_pinned flag in channel_posts
	if chatType == "channel" {
		if err := s.channelRepo.PinPost(ctx, messageID, true); err != nil {
			s.logger.Warn("Failed to update is_pinned in channel_posts", zap.Error(err))
		}
	}

	s.logger.Info("Message pinned",
		zap.String("chat_id", chatID.String()),
		zap.String("message_id", messageID.String()),
		zap.Int64("pinned_by", userID),
	)

	// Broadcast update to all participants in real time
	go s.broadcastPinnedMessagesUpdate(context.Background(), chatID, chatType)

	return nil
}

// UnpinMessage unpins a message
func (s *PinnedService) UnpinMessage(ctx context.Context, chatID uuid.UUID, chatType string, messageID uuid.UUID, userID int64) error {
	// Verify user has permission
	switch chatType {
	case "group":
		// Any member can unpin in groups
		isMember, err := s.groupRepo.IsMember(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check member", err)
		}
		if !isMember {
			return apperrors.NotGroupMember()
		}
	case "channel":
		role, err := s.channelRepo.GetMemberRole(ctx, chatID, userID)
		if err != nil {
			return apperrors.NotParticipant()
		}
		if role != "owner" && role != "admin" {
			return apperrors.Forbidden("Only channel admins can unpin messages")
		}
	case "personal":
		isParticipant, err := s.chatRepo.IsParticipant(ctx, chatID, userID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check participant", err)
		}
		if !isParticipant {
			return apperrors.NotParticipant()
		}
	default:
		return apperrors.BadRequest("invalid chat type")
	}

	belongs, err := s.pinnedRepo.MessageBelongsToChat(ctx, chatID, chatType, messageID)
	if err != nil {
		return apperrors.InternalWithErr("failed to validate pinned message", err)
	}
	if !belongs {
		return apperrors.BadRequest("message does not belong to the specified chat")
	}

	if err := s.pinnedRepo.UnpinMessage(ctx, messageID); err != nil {
		return apperrors.InternalWithErr("failed to unpin message", err)
	}

	// For channels, also update the is_pinned flag in channel_posts
	if chatType == "channel" {
		if err := s.channelRepo.PinPost(ctx, messageID, false); err != nil {
			s.logger.Warn("Failed to update is_pinned in channel_posts", zap.Error(err))
		}
	}

	s.logger.Info("Message unpinned",
		zap.String("message_id", messageID.String()),
	)

	// Broadcast update to all participants in real time
	go s.broadcastPinnedMessagesUpdate(context.Background(), chatID, chatType)

	return nil
}

// GetPinnedMessages gets pinned messages for chat
func (s *PinnedService) GetPinnedMessages(ctx context.Context, chatID uuid.UUID, chatType string, userID int64) (interface{}, error) {
	// Verify access
	switch chatType {
	case "personal":
		isParticipant, err := s.chatRepo.IsParticipant(ctx, chatID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to check participant", err)
		}
		if !isParticipant {
			return nil, apperrors.NotParticipant()
		}
	case "group":
		isMember, err := s.groupRepo.IsMember(ctx, chatID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to check member", err)
		}
		if !isMember {
			return nil, apperrors.NotGroupMember()
		}
	case "channel":
		isSubscribed, err := s.channelRepo.IsSubscribed(ctx, chatID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to check channel subscription", err)
		}
		if !isSubscribed {
			return nil, apperrors.NotParticipant()
		}
	}

	pinnedMessages, err := s.pinnedRepo.GetPinnedMessages(ctx, chatID, chatType)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get pinned messages", err)
	}

	return pinnedMessages, nil
}

// broadcastPinnedMessagesUpdate broadcasts the pinned messages list update to all participants in the chat
func (s *PinnedService) broadcastPinnedMessagesUpdate(ctx context.Context, chatID uuid.UUID, chatType string) {
	if s.hub == nil {
		return
	}

	var userIDs []int64
	var err error

	switch chatType {
	case "personal":
		userIDs, err = s.chatRepo.GetConversationParticipants(ctx, chatID)
	case "group":
		userIDs, err = s.groupRepo.GetGroupMembers(ctx, chatID)
	case "channel":
		userIDs, err = s.channelRepo.GetChannelSubscriberIDs(ctx, chatID)
	}

	if err != nil {
		s.logger.Error("Failed to get participants for pin broadcast",
			zap.Error(err),
			zap.String("chat_id", chatID.String()),
			zap.String("chat_type", chatType),
		)
		return
	}

	msg := &websocket.Message{
		Type:           "pinned_messages_updated",
		ConversationID: &chatID,
		Content: map[string]interface{}{
			"chat_type": chatType,
		},
		Timestamp: time.Now().Unix(),
	}

	s.hub.SendToUsers(userIDs, msg)
}

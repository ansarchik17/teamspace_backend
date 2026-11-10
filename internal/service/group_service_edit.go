package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

// EditGroupMessage edits a message in a group
func (s *GroupService) EditGroupMessage(ctx context.Context, messageID uuid.UUID, userID int64, newContent string) error {
	// Get message
	messageView, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return apperrors.MessageNotFound()
	}

	// Check if user is sender
	if messageView.SenderID != userID {
		return apperrors.CannotEditMessage()
	}

	// Verify the message belongs to a group (conversation_id should be a group_id)
	// and that the user is a member of that group
	isMember, err := s.groupRepo.IsMember(ctx, messageView.ConversationID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return apperrors.NotGroupMember()
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
		// Send WebSocket notification to all group members
		members, err := s.groupRepo.GetGroupMembers(ctx, messageView.ConversationID)
		if err == nil {
			for _, memberID := range members {
				// Send to all members including sender (so all their devices get the update)
				if s.hub.IsUserOnline(memberID) {
					s.hub.SendMessageEditNotification(memberID, updatedMessageView)
				}
			}
		}
	}

	s.logger.Info("Group message edited",
		zap.String("message_id", messageID.String()),
		zap.String("group_id", messageView.ConversationID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

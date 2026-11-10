package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

type BroadcastMessageRequest struct {
	RecipientUserIDs []int64 `json:"recipient_user_ids" binding:"required,min=1"`
	Content          string  `json:"content" binding:"required,min=1"`
	MessageType      string  `json:"message_type" binding:"omitempty,oneof=text image video file audio"`
}

type BroadcastMessageResponse struct {
	SuccessCount int                      `json:"success_count"`
	FailureCount int                      `json:"failure_count"`
	Results      []BroadcastMessageResult `json:"results"`
}

type BroadcastMessageResult struct {
	UserID         int64      `json:"user_id"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
	MessageID      *uuid.UUID `json:"message_id,omitempty"`
	Success        bool       `json:"success"`
	Error          *string    `json:"error,omitempty"`
}

// BroadcastMessage sends the same message to multiple users via individual conversations
func (s *ChatService) BroadcastMessage(ctx context.Context, senderID int64, req *BroadcastMessageRequest) (*BroadcastMessageResponse, error) {
	if req == nil {
		return nil, apperrors.BadRequest("empty request")
	}

	messageType := strings.ToLower(strings.TrimSpace(req.MessageType))
	if messageType == "" {
		messageType = "text"
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, apperrors.BadRequest("message content cannot be empty")
	}

	if len(req.RecipientUserIDs) == 0 {
		return nil, apperrors.BadRequest("at least one recipient is required")
	}

	if len(req.RecipientUserIDs) > 100 {
		return nil, apperrors.BadRequest("maximum 100 recipients allowed per broadcast")
	}

	uniqueRecipients := make(map[int64]struct{})
	for _, userID := range req.RecipientUserIDs {
		if userID > 0 && userID != senderID {
			uniqueRecipients[userID] = struct{}{}
		}
	}

	if len(uniqueRecipients) == 0 {
		return nil, apperrors.BadRequest("no valid recipients found")
	}

	response := &BroadcastMessageResponse{
		SuccessCount: 0,
		FailureCount: 0,
		Results:      make([]BroadcastMessageResult, 0, len(uniqueRecipients)),
	}

	s.logger.Info("Starting message broadcast",
		zap.Int64("sender_id", senderID),
		zap.Int("recipient_count", len(uniqueRecipients)),
		zap.String("message_type", messageType),
	)

	// Send message to each recipient
	for recipientID := range uniqueRecipients {
		result := s.sendBroadcastMessageToUser(ctx, senderID, recipientID, content, messageType)
		response.Results = append(response.Results, result)

		if result.Success {
			response.SuccessCount++
		} else {
			response.FailureCount++
		}
	}

	s.logger.Info("Message broadcast completed",
		zap.Int64("sender_id", senderID),
		zap.Int("success_count", response.SuccessCount),
		zap.Int("failure_count", response.FailureCount),
	)

	return response, nil
}

func (s *ChatService) sendBroadcastMessageToUser(ctx context.Context, senderID, recipientID int64, content, messageType string) BroadcastMessageResult {
	result := BroadcastMessageResult{
		UserID:  recipientID,
		Success: false,
	}

	conversation, err := s.CreateOrGetConversation(ctx, senderID, recipientID)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create conversation: %v", err)
		result.Error = &errMsg
		s.logger.Warn("Broadcast: failed to create conversation",
			zap.Int64("sender_id", senderID),
			zap.Int64("recipient_id", recipientID),
			zap.Error(err),
		)
		return result
	}

	result.ConversationID = &conversation.ID

	messageView, err := s.SendMessage(ctx, conversation.ID, senderID, content, "", nil, messageType, nil, nil, false, nil, nil, "")
	if err != nil {
		errMsg := fmt.Sprintf("failed to send message: %v", err)
		result.Error = &errMsg
		s.logger.Warn("Broadcast: failed to send message",
			zap.Int64("sender_id", senderID),
			zap.Int64("recipient_id", recipientID),
			zap.String("conversation_id", conversation.ID.String()),
			zap.Error(err),
		)
		return result
	}

	result.MessageID = &messageView.ID
	result.Success = true

	return result
}

package service

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// MessageSyncService handles message synchronization logic
type MessageSyncService struct {
	syncRepo *repository.MessageSyncRepository
	logger   *zap.Logger
}

func NewMessageSyncService(
	syncRepo *repository.MessageSyncRepository,
	logger *zap.Logger,
) *MessageSyncService {
	return &MessageSyncService{
		syncRepo: syncRepo,
		logger:   logger.With(zap.String("service", "message_sync")),
	}
}

// SyncRequest represents a synchronization request
type SyncRequest struct {
	UserID         int64
	DeviceID       string
	ConversationID *uuid.UUID // nil for global sync
	AfterSequence  *int64     // nil uses the last client-acknowledged server cursor
	Limit          int        // max messages to return
}

// SyncResponse represents the result of a synchronization
type SyncResponse struct {
	Messages       []*domain.MessageView `json:"messages"`
	HasMore        bool                  `json:"has_more"`
	LastSequence   int64                 `json:"last_sequence"`
	SyncedAt       time.Time             `json:"synced_at"`
	TotalAvailable int                   `json:"total_available,omitempty"`
}

// SyncMessages synchronizes messages for a device after reconnection
func (s *MessageSyncService) SyncMessages(ctx context.Context, req SyncRequest) (*SyncResponse, error) {
	if !IsValidSyncDeviceID(req.DeviceID) {
		return nil, fmt.Errorf("invalid device_id")
	}
	if req.Limit <= 0 {
		req.Limit = 100 // default limit
	}
	if req.Limit > 500 {
		req.Limit = 500 // max limit to prevent overload
	}

	s.logger.Info("Starting message sync",
		zap.Int64("user_id", req.UserID),
		zap.String("device_id", req.DeviceID),
		zap.Int("limit", req.Limit),
	)

	cursor, err := s.syncRepo.GetOrCreateSyncCursor(ctx, req.UserID, req.DeviceID, req.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("get sync cursor: %w", err)
	}
	afterSequence := cursor.LastSequenceNumber
	if req.AfterSequence != nil {
		if *req.AfterSequence < 0 {
			return nil, fmt.Errorf("after_sequence must be non-negative")
		}
		afterSequence = *req.AfterSequence
		// The request cursor is an acknowledgement of data already applied by the
		// client. Never acknowledge the batch before it has reached the client.
		if err := s.syncRepo.UpdateSyncCursor(ctx, req.UserID, req.DeviceID, req.ConversationID, afterSequence); err != nil {
			return nil, fmt.Errorf("acknowledge sync cursor: %w", err)
		}
	}

	// Get missed messages since the client-acknowledged position.
	messages, err := s.syncRepo.GetMissedMessages(ctx, req.UserID, req.ConversationID, afterSequence, req.Limit+1)
	if err != nil {
		s.logger.Error("Failed to get missed messages", zap.Error(err))
		return nil, fmt.Errorf("sync messages: %w", err)
	}

	// Check if there are more messages available
	hasMore := len(messages) > req.Limit
	if hasMore {
		messages = messages[:req.Limit]
	}

	// Get the last sequence number
	lastSeq := afterSequence
	if len(messages) > 0 {
		lastSeq = messages[len(messages)-1].SequenceNumber
	}

	response := &SyncResponse{
		Messages:     messages,
		HasMore:      hasMore,
		LastSequence: lastSeq,
		SyncedAt:     time.Now(),
	}

	s.logger.Info("Message sync completed",
		zap.Int64("user_id", req.UserID),
		zap.String("device_id", req.DeviceID),
		zap.Int("messages_count", len(messages)),
		zap.Bool("has_more", hasMore),
	)

	return response, nil
}

// HandleReconnection handles WebSocket reconnection and returns missed messages
func (s *MessageSyncService) HandleReconnection(ctx context.Context, userID int64, deviceID string, lastKnownSequence int64, limit int) (interface{}, error) {
	s.logger.Info("Handling reconnection",
		zap.Int64("user_id", userID),
		zap.String("device_id", deviceID),
		zap.Int64("last_known_sequence", lastKnownSequence),
	)

	// Sync all conversations (global sync)
	req := SyncRequest{
		UserID:         userID,
		DeviceID:       deviceID,
		ConversationID: nil, // global sync
		AfterSequence:  &lastKnownSequence,
		Limit:          limit,
	}

	return s.SyncMessages(ctx, req)
}

// UpdateSyncPosition manually updates the sync position for a device
func (s *MessageSyncService) UpdateSyncPosition(ctx context.Context, userID int64, deviceID string, conversationID *uuid.UUID, sequence int64) error {
	if !IsValidSyncDeviceID(deviceID) || sequence < 0 {
		return fmt.Errorf("invalid sync position")
	}
	if _, err := s.syncRepo.GetOrCreateSyncCursor(ctx, userID, deviceID, conversationID); err != nil {
		return err
	}
	return s.syncRepo.UpdateSyncCursor(ctx, userID, deviceID, conversationID, sequence)
}

var syncDeviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func IsValidSyncDeviceID(deviceID string) bool {
	return syncDeviceIDPattern.MatchString(deviceID)
}

// GetSyncStatus returns the current sync status for a device
func (s *MessageSyncService) GetSyncStatus(ctx context.Context, userID int64, deviceID string, conversationID *uuid.UUID) (*repository.SyncCursor, error) {
	return s.syncRepo.GetOrCreateSyncCursor(ctx, userID, deviceID, conversationID)
}

// CleanupOldSyncData removes old sync cursors (cleanup job)
func (s *MessageSyncService) CleanupOldSyncData(ctx context.Context) error {
	// Remove cursors not updated in 30 days
	deleted, err := s.syncRepo.CleanupOldCursors(ctx, 30*24*time.Hour)
	if err != nil {
		return fmt.Errorf("cleanup old sync data: %w", err)
	}

	s.logger.Info("Cleaned up old sync cursors", zap.Int64("deleted", deleted))
	return nil
}

// DeduplicateMessages helps client deduplicate messages based on sequence numbers
// This is a helper that can be called by the client to verify uniqueness
func (s *MessageSyncService) DeduplicateMessages(messages []*domain.MessageView) []*domain.MessageView {
	seen := make(map[uuid.UUID]bool)
	unique := make([]*domain.MessageView, 0, len(messages))

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if !seen[msg.ID] {
			seen[msg.ID] = true
			unique = append(unique, msg)
		}
	}

	if len(unique) < len(messages) {
		s.logger.Info("Deduplicated messages",
			zap.Int("original", len(messages)),
			zap.Int("unique", len(unique)),
		)
	}

	return unique
}

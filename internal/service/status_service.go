package service

import (
	"context"
	"time"

	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type StatusService struct {
	statusRepo *repository.StatusRepository
	hub        *websocket.Hub
	logger     *zap.Logger
}

func NewStatusService(
	statusRepo *repository.StatusRepository,
	hub *websocket.Hub,
	logger *zap.Logger,
) *StatusService {
	return &StatusService{
		statusRepo: statusRepo,
		hub:        hub,
		logger:     logger.With(zap.String("service", "status")),
	}
}

// BroadcastUserOnline notifies interested users that a user came online
func (s *StatusService) BroadcastUserOnline(ctx context.Context, userID int64) {
	s.broadcastStatus(ctx, userID, "online")
}

// BroadcastUserOffline notifies interested users that a user went offline
func (s *StatusService) BroadcastUserOffline(ctx context.Context, userID int64) {
	if s.hub.IsUserOnline(userID) {
		return
	}
	s.broadcastStatus(ctx, userID, "offline")
}

// broadcastStatus sends user status to interested users only
func (s *StatusService) broadcastStatus(ctx context.Context, userID int64, status string) {
	// Get list of users who should receive this status update
	interestedUsers, err := s.statusRepo.GetInterestedUsers(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get interested users",
			zap.Error(err),
			zap.Int64("user_id", userID),
		)
		return
	}

	if len(interestedUsers) == 0 {
		s.logger.Debug("No interested users for status update",
			zap.Int64("user_id", userID),
			zap.String("status", status),
		)
		return
	}

	// Send status update to interested users via WebSocket
	s.hub.SendUserStatusToInterested(userID, status, interestedUsers)

	s.logger.Info("User status broadcasted",
		zap.Int64("user_id", userID),
		zap.String("status", status),
		zap.Int("interested_users_count", len(interestedUsers)),
	)
}

// GetOnlineUsersForConversations returns online status for specific users
// Used when loading conversation list to show initial online/offline status
func (s *StatusService) GetOnlineUsersForConversations(ctx context.Context, userIDs []int64) map[int64]bool {
	result := make(map[int64]bool)

	for _, userID := range userIDs {
		result[userID] = s.hub.IsUserOnline(userID)
	}

	return result
}

// GetInitialOnlineUsers returns the list of currently online interested users
func (s *StatusService) GetInitialOnlineUsers(ctx context.Context, userID int64) ([]int64, error) {
	interestedUsers, err := s.statusRepo.GetInterestedUsers(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get interested users for initial status",
			zap.Error(err),
			zap.Int64("user_id", userID),
		)
		return nil, err
	}

	var onlineUsers []int64
	for _, targetID := range interestedUsers {
		if s.hub.IsUserOnline(targetID) {
			onlineUsers = append(onlineUsers, targetID)
		}
	}

	return onlineUsers, nil
}

// SendInitialStatuses sends the list of currently online interested users to the connect user
// Deprecated: Use GetInitialOnlineUsers and send directly to avoid race conditions
func (s *StatusService) SendInitialStatuses(ctx context.Context, userID int64) {
	onlineUsers, err := s.GetInitialOnlineUsers(ctx, userID)
	if err != nil || len(onlineUsers) == 0 {
		return
	}

	// We send a custom event with list of online user IDs
	msg := &websocket.Message{
		Type:      "initial_online_list",
		To:        userID,
		Content:   onlineUsers,
		Timestamp: time.Now().Unix(),
	}

	s.hub.SendToUser(userID, msg)
}

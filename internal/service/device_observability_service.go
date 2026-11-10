package service

import (
	"context"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type DeviceObservabilityService struct {
	deviceRepo       *repository.DeviceRepository
	userRepo         *repository.UserRepository
	loginLogRepo     *repository.LoginLogRepository
	kcAdmin          *keycloak.AdminClient
	realm            string
	allowedClientIDs []string
	hub              *websocket.Hub
	logger           *zap.Logger
}

func NewDeviceObservabilityService(
	deviceRepo *repository.DeviceRepository,
	userRepo *repository.UserRepository,
	loginLogRepo *repository.LoginLogRepository,
	kcAdmin *keycloak.AdminClient,
	realm string,
	allowedClientIDs []string,
	hub *websocket.Hub,
	logger *zap.Logger,
) *DeviceObservabilityService {
	return &DeviceObservabilityService{
		deviceRepo:       deviceRepo,
		userRepo:         userRepo,
		loginLogRepo:     loginLogRepo,
		kcAdmin:          kcAdmin,
		realm:            realm,
		allowedClientIDs: allowedClientIDs,
		hub:              hub,
		logger:           logger.With(zap.String("service", "device_observability")),
	}
}
func (s *DeviceObservabilityService) SaveMetadata(ctx context.Context, userID int64, device *domain.UserDevice) error {
	device.UserID = userID
	if err := s.deviceRepo.SaveDeviceMetadata(ctx, device); err != nil {
		return err
	}
	// Update user's last_seen_at in users table
	if err := s.userRepo.UpdateLastSeen(ctx, userID, time.Now().UTC()); err != nil {
		s.logger.Warn("failed to update user last seen during metadata save", zap.Int64("user_id", userID), zap.Error(err))
	}
	return nil
}

func (s *DeviceObservabilityService) ListUsers(
	ctx context.Context,
	query string,
	platform string,
	deviceType string,
	isOnline *bool,
	appVersion string,
	limit, offset int,
) ([]*domain.AdminUserListItem, int, error) {
	// Fetch online user list
	onlineUserIDsMap, err := s.getOnlineUserIDs(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch online users from Keycloak/Hub, continuing without it", zap.Error(err))
		onlineUserIDsMap = make(map[int64]bool)
	}

	onlineUserIDsList := make([]int64, 0, len(onlineUserIDsMap))
	for uid := range onlineUserIDsMap {
		onlineUserIDsList = append(onlineUserIDsList, uid)
	}

	items, total, err := s.deviceRepo.GetAdminUserList(ctx, query, platform, deviceType, isOnline, appVersion, onlineUserIDsList, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	// Enrich items with IsOnline status
	for _, item := range items {
		item.IsOnline = s.isUserOnline(item.UserID, item.LastSeenAt, onlineUserIDsMap)
	}

	return items, total, nil
}

func (s *DeviceObservabilityService) getOnlineUserIDs(ctx context.Context) (map[int64]bool, error) {
	onlineIDs := make(map[int64]bool)

	// 1. WebSocket hub online users
	for _, uid := range s.hub.GetConnectedUsers() {
		onlineIDs[uid] = true
	}

	// 2. Keycloak active sessions
	if s.kcAdmin != nil {
		for _, clientID := range s.allowedClientIDs {
			if clientID == "" {
				continue
			}
			clientUUID, err := s.kcAdmin.GetClientUUIDByClientID(ctx, s.realm, clientID)
			if err != nil {
				s.logger.Debug("failed to resolve Keycloak client UUID during online users list", zap.String("client_id", clientID), zap.Error(err))
				continue
			}
			sessions, err := s.kcAdmin.ListClientUserSessions(ctx, s.realm, clientUUID)
			if err != nil {
				s.logger.Debug("failed to list Keycloak client sessions during online users list", zap.String("client_id", clientID), zap.Error(err))
				continue
			}
			kcIDs := make([]string, 0, len(sessions))
			for _, sess := range sessions {
				if sess.UserID != "" {
					kcIDs = append(kcIDs, sess.UserID)
				}
			}
			if len(kcIDs) > 0 {
				users, err := s.userRepo.GetByKeycloakIDs(ctx, kcIDs)
				if err == nil {
					for _, u := range users {
						onlineIDs[u.ID] = true
					}
				}
			}
		}
	}

	return onlineIDs, nil
}

func (s *DeviceObservabilityService) isUserOnline(userID int64, lastSeenAt *time.Time, onlineUserIDs map[int64]bool) bool {
	if onlineUserIDs[userID] {
		return true
	}
	if lastSeenAt != nil && time.Since(*lastSeenAt) <= 5*time.Minute {
		return true
	}
	return false
}

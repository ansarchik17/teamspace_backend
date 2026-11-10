package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const (
	defaultUserQRTTL = 2 * time.Minute
	maxUserQRTTL     = 10 * time.Minute
)

type userQRUserRepository interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

type userQRConversationRepository interface {
	GetConversationByParticipants(ctx context.Context, user1ID, user2ID int64) (*domain.Conversation, error)
}

type CreateUserQRCodeRequest struct {
	TTLSeconds *int `json:"ttl_seconds,omitempty"`
}

type UserQRPayload struct {
	Type      string    `json:"type"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type UserQRCodeResponse struct {
	Token     string        `json:"token"`
	ExpiresAt time.Time     `json:"expires_at"`
	QRValue   string        `json:"qr_value"`
	QRPayload UserQRPayload `json:"qr_payload"`
}

type ScanUserQRCodeRequest struct {
	Token string `json:"token"`
}

type ScannedUserQRCodeResponse struct {
	User           *ScannedQRCodeUser `json:"user"`
	ExpiresAt      time.Time          `json:"expires_at"`
	HasChat        bool               `json:"has_chat"`
	ConversationID *uuid.UUID         `json:"conversation_id,omitempty"`
}

type ScannedQRCodeUser struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	FirstName          string     `json:"first_name"`
	LastName           string     `json:"last_name"`
	FullName           string     `json:"full_name"`
	AvatarURL          *string    `json:"avatar_url,omitempty"`
	Position           *string    `json:"position,omitempty"`
	Bio                *string    `json:"bio,omitempty"`
	AvailabilityStatus *string    `json:"availability_status"`
	LastSeenAt         *time.Time `json:"last_seen_at,omitempty"`
}

type UserQRService struct {
	repo             *repository.UserQRRepository
	userRepo         userQRUserRepository
	conversationRepo userQRConversationRepository
	logger           *zap.Logger
	now              func() time.Time
}

func NewUserQRService(
	repo *repository.UserQRRepository,
	userRepo userQRUserRepository,
	conversationRepo userQRConversationRepository,
	logger *zap.Logger,
) *UserQRService {
	return &UserQRService{
		repo:             repo,
		userRepo:         userRepo,
		conversationRepo: conversationRepo,
		logger:           logger.With(zap.String("service", "user_qr")),
		now:              time.Now,
	}
}

func (s *UserQRService) CreateOrRefresh(ctx context.Context, userID int64, req *CreateUserQRCodeRequest) (*UserQRCodeResponse, error) {
	if _, err := s.ensureUserAvailable(ctx, userID); err != nil {
		return nil, err
	}

	ttl := defaultUserQRTTL
	if req != nil && req.TTLSeconds != nil && *req.TTLSeconds > 0 {
		ttl = time.Duration(*req.TTLSeconds) * time.Second
	}
	if ttl > maxUserQRTTL {
		ttl = maxUserQRTTL
	}

	now := s.now()
	if err := s.repo.RevokeActiveTokensForUser(ctx, userID, now); err != nil {
		return nil, apperrors.InternalWithErr("failed to revoke previous qr code", err)
	}

	tokenValue, err := generateOpaqueSecret(24)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to generate qr token", err)
	}

	token := &repository.UserQRToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: hashSecret(tokenValue),
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	if err := s.repo.Create(ctx, token); err != nil {
		return nil, apperrors.InternalWithErr("failed to create qr token", err)
	}

	payload := UserQRPayload{
		Type:      "user_qr",
		Token:     tokenValue,
		ExpiresAt: token.ExpiresAt,
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to prepare qr payload", err)
	}

	return &UserQRCodeResponse{
		Token:     tokenValue,
		ExpiresAt: token.ExpiresAt,
		QRValue:   string(rawPayload),
		QRPayload: payload,
	}, nil
}

func (s *UserQRService) Scan(ctx context.Context, scannerUserID int64, rawToken string) (*ScannedUserQRCodeResponse, error) {
	tokenValue := strings.TrimSpace(rawToken)
	if tokenValue == "" {
		return nil, apperrors.Validation("token is required")
	}

	now := s.now()
	token, err := s.repo.GetActiveByTokenHash(ctx, hashSecret(tokenValue), now)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to validate qr token", err)
	}
	if token == nil {
		return nil, apperrors.NotFound("qr token")
	}
	if token.UserID == scannerUserID {
		return nil, apperrors.BadRequest("cannot scan your own qr code")
	}

	user, err := s.ensureUserAvailable(ctx, token.UserID)
	if err != nil {
		return nil, err
	}

	resp := &ScannedUserQRCodeResponse{
		User:      buildScannedQRCodeUser(user),
		ExpiresAt: token.ExpiresAt,
	}

	if s.conversationRepo != nil {
		conversation, err := s.conversationRepo.GetConversationByParticipants(ctx, scannerUserID, token.UserID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to resolve existing conversation", err)
		}
		if conversation != nil {
			resp.HasChat = true
			resp.ConversationID = &conversation.ID
		}
	}

	return resp, nil
}

func (s *UserQRService) ensureUserAvailable(ctx context.Context, userID int64) (*domain.User, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, apperrors.NotFound("user")
	}
	if !user.IsActive {
		return nil, apperrors.Forbidden("user is inactive")
	}
	return user, nil
}

func buildScannedQRCodeUser(user *domain.User) *ScannedQRCodeUser {
	if user == nil {
		return nil
	}

	return &ScannedQRCodeUser{
		ID:                 user.ID,
		Username:           user.Username,
		FirstName:          user.FirstName,
		LastName:           user.LastName,
		FullName:           strings.TrimSpace(user.FirstName + " " + user.LastName),
		AvatarURL:          user.AvatarURL,
		Position:           user.Position,
		Bio:                user.Bio,
		AvailabilityStatus: user.AvailabilityStatus,
		LastSeenAt:         user.LastSeenAt,
	}
}

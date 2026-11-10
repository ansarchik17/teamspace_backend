package service

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

type MeetingService struct {
	config config.JitsiConfig
	logger *zap.Logger
}

func NewMeetingService(config config.JitsiConfig, logger *zap.Logger) *MeetingService {
	return &MeetingService{
		config: config,
		logger: logger.With(zap.String("service", "meeting")),
	}
}

// JitsiClaims defines the custom claims required by Jitsi Meet
type JitsiClaims struct {
	jwt.RegisteredClaims
	Room    string       `json:"room"`
	Context JitsiContext `json:"context"`
}

type JitsiContext struct {
	User     JitsiUser     `json:"user"`
	Features JitsiFeatures `json:"features"`
}

type JitsiUser struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Avatar    string `json:"avatar"`
	Moderator string `json:"moderator,omitempty"` // "true" if admin/moderator
}

type JitsiFeatures struct {
	Recording     string `json:"recording"`
	Livestreaming string `json:"livestreaming"`
	ScreenSharing string `json:"screen-sharing"`
}

// GenerateMeetingLink generates a signed Jitsi Meet URL
func (s *MeetingService) GenerateMeetingLink(roomName, userName, userEmail, userAvatar string, isAdmin bool) (string, error) {
	now := time.Now()

	claims := JitsiClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"jitsi"},
			Issuer:    s.config.AppID,
			Subject:   s.config.Domain,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
		},
		Room: roomName,
		Context: JitsiContext{
			User: JitsiUser{
				Name:   userName,
				Email:  userEmail,
				Avatar: userAvatar,
			},
			Features: JitsiFeatures{
				ScreenSharing: "true",
			},
		},
	}

	// Set moderator privileges
	if isAdmin {
		claims.Context.User.Moderator = "true"
		claims.Context.Features.Recording = "true"
		claims.Context.Features.Livestreaming = "true"
	} else {
		claims.Context.Features.Recording = "false"
		claims.Context.Features.Livestreaming = "false"
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(s.config.AppSecret))
	if err != nil {
		s.logger.Error("Failed to sign Jitsi token", zap.Error(err))
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	link := fmt.Sprintf("https://%s/%s?jwt=%s", s.config.Domain, roomName, signedToken)
	return link, nil
}

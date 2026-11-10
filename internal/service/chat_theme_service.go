package service

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type ChatThemeService struct {
	repo   *repository.ChatThemeRepository
	logger *zap.Logger
}

func NewChatThemeService(repo *repository.ChatThemeRepository, logger *zap.Logger) *ChatThemeService {
	return &ChatThemeService{
		repo:   repo,
		logger: logger.With(zap.String("service", "chat_theme")),
	}
}

// SetTheme saves a theme for a user
func (s *ChatThemeService) SetTheme(ctx context.Context, userID int64, req domain.UpdateChatThemeRequest) (*domain.ChatTheme, error) {
	theme := &domain.ChatTheme{
		UserID:             userID,
		MessageColorHex:    req.Theme.MessageColorHex,
		BackgroundType:     req.Theme.BackgroundType,
		BackgroundColorHex: req.Theme.BackgroundColorHex,
		BackgroundImageURL: req.Theme.BackgroundImageURL,
	}

	err := s.repo.Upsert(ctx, theme)
	if err != nil {
		return nil, err
	}

	return theme, nil
}

// GetTheme retrieves a chat theme for a user
func (s *ChatThemeService) GetTheme(ctx context.Context, userID int64) (*domain.ChatTheme, error) {
	return s.repo.Get(ctx, userID)
}

// DeleteTheme removes a custom theme, reverting to default
func (s *ChatThemeService) DeleteTheme(ctx context.Context, userID int64) error {
	return s.repo.Delete(ctx, userID)
}

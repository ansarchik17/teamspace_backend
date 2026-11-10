package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type UpdateLanguageRequest struct {
	Language string `json:"language" binding:"required,oneof=ru kk"`
}

type userLanguageRepository interface {
	UpdateLanguage(ctx context.Context, userID int64, language string) error
}

func (s *Service) UpdateLanguage(ctx context.Context, userID int64, language string) (*domain.UserResponse, error) {
	if language != "ru" && language != "kk" {
		return nil, apperrors.BadRequest("language must be ru or kk")
	}
	repo, ok := s.userRepo.(userLanguageRepository)
	if !ok {
		return nil, apperrors.Internal("user repository does not support language preferences")
	}
	if err := repo.UpdateLanguage(ctx, userID, language); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.NotFound("User not found")
		}
		return nil, apperrors.InternalWithErr("failed to save user language", err)
	}
	return s.GetCurrentUser(ctx, userID)
}

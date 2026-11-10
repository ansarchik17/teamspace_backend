package auth

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

type userLanguageLookup interface {
	GetLanguage(context.Context, int64) (string, error)
}

func (s *Service) GetUserLanguage(ctx context.Context, userID int64) (string, error) {
	if repo, ok := s.userRepo.(userLanguageLookup); ok {
		return repo.GetLanguage(ctx, userID)
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if user == nil {
		return string(i18n.Russian), nil
	}
	return user.Language, nil
}

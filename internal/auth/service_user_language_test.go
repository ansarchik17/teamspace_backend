package auth

import (
	"context"
	"testing"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestUpdateLanguageRejectsUnsupportedValues(t *testing.T) {
	for _, language := range []string{"", "en", "RU", "kk-KZ", " kk "} {
		t.Run(language, func(t *testing.T) {
			// No repository: invalid values must fail before touching persistence.
			user, err := (&Service{}).UpdateLanguage(context.Background(), 1, language)
			if user != nil || err == nil || apperrors.ToAppError(err).StatusCode != 400 {
				t.Fatalf("expected bad request, got user=%v err=%v", user, err)
			}
		})
	}
}

type languageRepoStub struct{ meUserRepoStub }

func (r *languageRepoStub) UpdateLanguage(_ context.Context, userID int64, language string) error {
	if userID != r.user.ID {
		return apperrors.NotFound("User")
	}
	r.user.Language = language
	if r.details != nil {
		r.details.Language = language
	}
	return nil
}

func TestUpdateLanguageReturnsSavedProfile(t *testing.T) {
	for _, fullDetails := range []bool{false, true} {
		repo := &languageRepoStub{meUserRepoStub: meUserRepoStub{user: &domain.User{ID: 7, Language: "ru"}}}
		if fullDetails {
			repo.details = &domain.UserDetailResponse{UserResponse: *repo.user.ToResponse()}
		}
		service := &Service{userRepo: repo}
		for _, language := range []string{"kk", "ru"} {
			profile, err := service.UpdateLanguage(context.Background(), 7, language)
			if err != nil || profile.Language != language {
				t.Fatalf("update %q: profile=%v err=%v", language, profile, err)
			}
			profile, err = service.GetCurrentUser(context.Background(), 7)
			if err != nil || profile.Language != language {
				t.Fatalf("read %q: profile=%v err=%v", language, profile, err)
			}
		}
	}
}

package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

type userCreationAuthorizationRepo struct {
	UserRepository
	allowed bool
	err     error
}

func (r *userCreationAuthorizationRepo) CanCreateUser(context.Context, int64) (bool, error) {
	return r.allowed, r.err
}

func TestUserCreationAdministrationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		allowed    bool
		err        error
		wantStatus int
	}{
		{"authorized administrator", true, nil, 0},
		{"ordinary employee", false, nil, http.StatusForbidden},
		{"permission lookup unavailable", true, errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{userRepo: &userCreationAuthorizationRepo{allowed: tc.allowed, err: tc.err}}
			err := svc.RequireUserCreationAdministration(context.Background(), 9)
			if tc.wantStatus == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var appErr *apperrors.AppError
			if !errors.As(err, &appErr) || appErr.StatusCode != tc.wantStatus {
				t.Fatalf("unexpected authorization result: %v", err)
			}
		})
	}
}

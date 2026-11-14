package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

func TestDeactivatedUserCannotUseIssuedTokens(t *testing.T) {
	ctx := context.Background()
	active := true
	tm := NewTokenManager(&config.JWTConfig{Secret: "test", Issuer: "test", Audience: "test", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour}, nil)
	tm.SetUserActiveLookup(func(context.Context, int64) (bool, error) { return active, nil })
	tokens, err := tm.GenerateTokenPair(ctx, 77, "", "", "", "", "", false, nil)
	require.NoError(t, err)
	_, err = tm.ValidateToken(ctx, tokens.AccessToken)
	require.NoError(t, err)
	active = false
	_, err = tm.ValidateToken(ctx, tokens.AccessToken)
	require.ErrorIs(t, err, ErrUserInactive)
	_, err = tm.ValidateToken(ctx, tokens.RefreshToken)
	require.Error(t, err)
	_, err = tm.RefreshAccessToken(ctx, tokens.RefreshToken)
	require.Error(t, err)
	_, err = tm.GenerateTokenPair(ctx, 77, "", "", "", "", "", false, nil)
	require.Error(t, err)
	// An unavailable membership database must not allow access.
	tm.SetUserActiveLookup(func(context.Context, int64) (bool, error) { return false, errors.New("database unavailable") })
	_, err = tm.ValidateToken(ctx, tokens.AccessToken)
	require.Error(t, err)
}

type offboardingAuthRepo struct{ updateUserAdminRepoStub }

func (r *offboardingAuthRepo) GetByEmail(context.Context, string) (*domain.User, error) {
	return r.user, nil
}
func (r *offboardingAuthRepo) IsUserActive(context.Context, int64) (bool, error) {
	return r.user.IsActive, nil
}
func (r *offboardingAuthRepo) CheckNotOffboarded(context.Context, int64) error {
	return apperrors.Forbidden("employee offboarded")
}

func TestOffboardingDisablesIdentityAndPreservesAdminBarrier(t *testing.T) {
	ctx := context.Background()
	kcID := "identity-77"
	disabled, loggedOut := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/master/protocol/openid-connect/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"admin-test","expires_in":60}`))
		case "/admin/realms/test/users/identity-77":
			require.Equal(t, http.MethodPut, r.Method)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, map[string]any{"enabled": false}, body)
			disabled++
			w.WriteHeader(204)
		case "/admin/realms/test/users/identity-77/logout":
			require.Equal(t, http.MethodPost, r.Method)
			loggedOut++
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	repo := &offboardingAuthRepo{updateUserAdminRepoStub: updateUserAdminRepoStub{user: &domain.User{ID: 77, KeycloakID: &kcID, IsActive: false}}}
	svc := NewKeycloakService(repo, nil, nil, nil, keycloak.NewAdmin(server.URL, "master", "admin", "test"), "test")
	require.NoError(t, svc.DisableOffboardedIdentity(ctx, 77))
	require.NoError(t, svc.DisableOffboardedIdentity(ctx, 77))
	require.Equal(t, 2, disabled)
	require.Equal(t, 2, loggedOut)
	active := true
	_, err := svc.UpdateUserAdmin(ctx, 1, 77, &UpdateUserRequest{IsActive: &active})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 2, disabled)
	_, err = svc.Login(ctx, &LoginRequest{Email: "user@example.com", Password: "test"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	repo.user.IsActive = true
	require.Error(t, svc.DisableOffboardedIdentity(ctx, 77))
	require.Equal(t, 2, disabled)
}

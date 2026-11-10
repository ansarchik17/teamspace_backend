package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type admissionLoginRepo struct {
	UserRepository
	allowed bool
	user    *domain.User
}

func (r *admissionLoginRepo) IsUserActive(context.Context, int64) (bool, error) {
	return r.allowed, nil
}
func (r *admissionLoginRepo) GetByKeycloakID(context.Context, string) (*domain.User, error) {
	return r.user, nil
}

func TestKeycloakRefreshCannotReturnSessionBeforeAdmissionOrAfterCancellation(t *testing.T) {
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "synthetic-hire", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("test-only-signing-key"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "synthetic-refresh", "expires_in": 3600, "token_type": "Bearer"}))
	}))
	defer server.Close()
	repo := &admissionLoginRepo{user: &domain.User{ID: 24, IsActive: true}}
	svc := NewKeycloakService(repo, nil, nil, nil, keycloak.New(server.URL, "test", "web", "secret"), nil, "test")
	for _, allowed := range []bool{false, true, false} {
		repo.allowed = allowed
		pair, err := svc.RefreshToken(context.Background(), "synthetic-provider-refresh")
		if allowed {
			require.NoError(t, err)
			require.NotNil(t, pair)
		} else {
			require.Error(t, err)
			require.Nil(t, pair, "provider refresh bypassed current admission")
		}
	}
}

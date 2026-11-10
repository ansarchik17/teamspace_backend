package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type offboardingMiddlewareRepo struct{ active bool }

func (r *offboardingMiddlewareRepo) IsUserActive(context.Context, int64) (bool, error) {
	return r.active, nil
}
func (*offboardingMiddlewareRepo) GetByKeycloakID(context.Context, string) (*domain.User, error) {
	return nil, nil
}
func (*offboardingMiddlewareRepo) UpsertFromKeycloak(context.Context, *domain.User) error { return nil }
func (*offboardingMiddlewareRepo) UpdateLastLogin(context.Context, int64, time.Time) error {
	return nil
}

func TestOffboardedLocalJWTDoesNotFallThroughToKeycloakSync(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &offboardingMiddlewareRepo{active: true}
	tm := auth.NewTokenManager(&config.JWTConfig{Secret: "test", Issuer: "test", Audience: "test", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour}, nil)
	tm.SetUserActiveLookup(repo.IsUserActive)
	pair, err := tm.GenerateTokenPair(context.Background(), 77, "", "", "", "", "", false, nil)
	require.NoError(t, err)
	router := gin.New()
	router.Use(NewKeycloakAuthMiddleware(nil, repo, zap.NewNop(), tm).Authenticate())
	router.GET("/private", func(c *gin.Context) { c.Status(200) })
	request := func() int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/private", nil)
		r.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		router.ServeHTTP(w, r)
		return w.Code
	}
	require.Equal(t, 200, request())
	repo.active = false
	require.Equal(t, 403, request())
}

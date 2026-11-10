package middleware

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBlockedLocalJWTDoesNotFallThroughToKeycloak(t *testing.T) {
	tm := auth.NewTokenManager(&config.JWTConfig{Secret: "test-secret", Issuer: "issuer", Audience: "audience", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour}, nil)
	pair, err := tm.GenerateTokenPair(context.Background(), 7, "", "", "", "", "", false, nil)
	require.NoError(t, err)
	tm.SetUserActiveLookup(func(context.Context, int64) (bool, error) { return false, nil })
	for _, mw := range []gin.HandlerFunc{NewAuthMiddleware(tm, zap.NewNop()).Authenticate(), NewKeycloakAuthMiddleware(nil, nil, zap.NewNop(), tm).Authenticate()} {
		r := gin.New()
		called := false
		r.GET("/protected", mw, func(c *gin.Context) { called = true; c.Status(200) })
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 403, w.Code)
		require.False(t, called)
		require.Contains(t, w.Body.String(), "FORBIDDEN")
	}
}

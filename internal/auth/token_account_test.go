package auth

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"testing"
	"time"
)

func TestPreviouslyIssuedTokensRejectBlockedAccount(t *testing.T) {
	tm := NewTokenManager(&config.JWTConfig{Secret: "test-secret", Issuer: "issuer", Audience: "audience", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour}, nil)
	active := true
	var lookupErr error
	tm.SetUserActiveLookup(func(_ context.Context, id int64) (bool, error) {
		require.Equal(t, int64(7), id)
		return active, lookupErr
	})
	pair, err := tm.GenerateTokenPair(context.Background(), 7, "", "employee@example.com", "employee", "First", "Last", false, nil)
	require.NoError(t, err)
	_, err = tm.ValidateToken(context.Background(), pair.AccessToken)
	require.NoError(t, err)
	active = false
	_, err = tm.ValidateToken(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, ErrUserInactive)
	_, err = tm.RefreshAccessToken(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, ErrUserInactive)
	_, err = tm.GenerateTokenPair(context.Background(), 7, "", "", "", "", "", false, nil)
	require.ErrorIs(t, err, ErrUserInactive)
	active = true
	_, err = tm.ValidateToken(context.Background(), pair.AccessToken)
	require.NoError(t, err)
	lookupErr = errors.New("database unavailable")
	_, err = tm.ValidateToken(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, ErrUserAccessLookup)
}

func TestCredentialCutoffSurvivesRefreshRace(t *testing.T) {
	ctx := context.Background()
	tm := NewTokenManager(&config.JWTConfig{Secret: "test-secret", Issuer: "issuer", Audience: "audience", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour}, nil)
	pair, err := tm.GenerateTokenPair(WithAuthenticationTime(ctx, time.Now().Add(-time.Hour)), 7, "", "", "", "", "", false, nil)
	require.NoError(t, err)
	calls := 0
	cutoff := time.Now().Add(-time.Minute)
	tm.SetCredentialLookup(func(_ context.Context, _ int64, at time.Time) (bool, error) {
		calls++
		if calls == 1 {
			return true, nil
		}
		return !at.Before(cutoff), nil
	})
	_, err = tm.RefreshAccessToken(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, ErrCredentialsRevoked, "a token validated before reset must not turn into a new session after reset")
	require.Equal(t, 2, calls)
}
func TestProviderSessionAuthenticationTimeControlsCutoff(t *testing.T) {
	ctx := context.Background()
	tm := NewTokenManager(&config.JWTConfig{}, nil)
	cutoff := time.Now().Add(-time.Minute)
	tm.SetCredentialLookup(func(_ context.Context, _ int64, at time.Time) (bool, error) { return !at.Before(cutoff), nil })
	require.ErrorIs(t, tm.CheckCredentialClaims(ctx, 7, map[string]any{"iat": float64(time.Now().Unix()), "auth_time": float64(time.Now().Add(-time.Hour).Unix())}), ErrCredentialsRevoked)
	require.NoError(t, tm.CheckCredentialClaims(ctx, 7, map[string]any{"iat": float64(time.Now().Unix())}))
	require.ErrorIs(t, tm.CheckCredentialClaims(ctx, 7, map[string]any{}), ErrInvalidClaims)
}

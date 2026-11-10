package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
)

var (
	ErrInvalidToken       = errors.New("invalid token")
	ErrExpiredToken       = errors.New("token expired")
	ErrInvalidClaims      = errors.New("invalid token claims")
	ErrTokenNotFound      = errors.New("token not found")
	ErrInvalidTokenType   = errors.New("invalid token type")
	ErrCredentialsRevoked = errors.New("credentials revoked")
	ErrUserInactive       = errors.New("user inactive")
	ErrUserAccessLookup   = errors.New("user access lookup failed")
)

type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken              string    `json:"access_token"`
	RefreshToken             string    `json:"refresh_token"`
	IDToken                  string    `json:"id_token,omitempty"`
	TokenType                string    `json:"token_type"`
	ExpiresIn                int64     `json:"expires_in"`
	ExpiresAt                time.Time `json:"expires_at"`
	MustChangePassword       bool      `json:"must_change_password,omitempty"`
	CannotSkipPasswordChange bool      `json:"cannot_skip_password_change,omitempty"`
}

// TokenManager handles JWT token operations
type TokenManager struct {
	cfg              *config.JWTConfig
	secret           []byte
	tokenStorage     TokenStorage // For token blacklisting/whitelisting
	userActiveLookup func(context.Context, int64) (bool, error)
	credentialLookup func(context.Context, int64, time.Time) (bool, error)
}

// Configure once at startup; every local access/refresh token checks live state.
func (tm *TokenManager) SetUserActiveLookup(lookup func(context.Context, int64) (bool, error)) {
	tm.userActiveLookup = lookup
}

func (tm *TokenManager) checkUserActive(ctx context.Context, id int64) error {
	if tm.userActiveLookup == nil {
		return nil
	}
	active, err := tm.userActiveLookup(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUserAccessLookup, err)
	}
	if !active {
		return ErrUserInactive
	}
	return nil
}

// Configured before serving requests. Database state invalidates even previously
// issued Keycloak JWTs and refresh tokens, independently of Redis availability.
func (tm *TokenManager) SetCredentialLookup(fn func(context.Context, int64, time.Time) (bool, error)) {
	tm.credentialLookup = fn
}
func (tm *TokenManager) CheckCredentials(ctx context.Context, id int64, issued time.Time) error {
	if tm.credentialLookup == nil {
		return nil
	}
	allowed, err := tm.credentialLookup(ctx, id, issued)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUserAccessLookup, err)
	}
	if !allowed {
		return ErrCredentialsRevoked
	}
	return nil
}

// AuthenticationTime is inherited when tokens are refreshed or exchanged.
// A refresh racing a password reset must not become a new authentication.
type authenticationTimeKey struct{}

func WithAuthenticationTime(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, authenticationTimeKey{}, at)
}
func AuthenticationTime(claims map[string]any) (time.Time, error) {
	c := jwt.MapClaims(claims)
	issued, err := c.GetIssuedAt()
	if err != nil || issued == nil {
		return time.Time{}, ErrInvalidClaims
	}
	if raw, ok := claims["auth_time"]; ok {
		at, err := jwt.MapClaims{"iat": raw}.GetIssuedAt()
		if err != nil || at == nil {
			return time.Time{}, ErrInvalidClaims
		}
		if at.Before(issued.Time) {
			issued = at
		}
	}
	return issued.Time, nil
}
func (tm *TokenManager) CheckCredentialClaims(ctx context.Context, id int64, claims map[string]any) error {
	if tm.credentialLookup == nil {
		return nil
	}
	at, err := AuthenticationTime(claims)
	if err != nil {
		return err
	}
	return tm.CheckCredentials(ctx, id, at)
}
func (claims *Claims) AuthenticationStartedAt() time.Time {
	if claims.AuthenticationTime > 0 {
		return time.Unix(claims.AuthenticationTime, 0)
	}
	if claims.IssuedAt != nil {
		return claims.IssuedAt.Time
	}
	return time.Time{}
}

// TokenStorage interface for storing tokens (can be implemented with Redis)
type TokenStorage interface {
	StoreToken(ctx context.Context, tokenID string, userID int64, expiresAt time.Time) error
	IsTokenValid(ctx context.Context, tokenID string) (bool, error)
	RevokeToken(ctx context.Context, tokenID string) error
	RevokeAllUserTokens(ctx context.Context, userID int64) error
}

// LinkedDeviceTokenStorage adds optional bookkeeping for device-scoped web sessions.
// Implementations may use these hooks to revoke only one linked browser instead of
// terminating every user session.
type LinkedDeviceTokenStorage interface {
	StoreTokenContext(ctx context.Context, tokenID string, userID int64, expiresAt time.Time, tokenCtx *Context) error
	RevokeTokensByLinkedDevice(ctx context.Context, userID int64, deviceID string) error
	RevokeWebLinkedTokensExceptDevice(ctx context.Context, userID int64, keepDeviceID string) error
}

// NewTokenManager creates a new token manager
func NewTokenManager(cfg *config.JWTConfig, storage TokenStorage) *TokenManager {
	return &TokenManager{
		cfg:          cfg,
		secret:       []byte(cfg.Secret),
		tokenStorage: storage,
	}
}

// GenerateTokenPair generates access and refresh token pair
func (tm *TokenManager) GenerateTokenPair(
	ctx context.Context,
	userID int64,
	iin, email, username, firstName, lastName string,
	hasPin bool,
	features map[string]bool,
	contexts ...*Context,
) (*TokenPair, error) {
	if err := tm.checkUserActive(ctx, userID); err != nil {
		return nil, err
	}

	now := time.Now()
	started := now.Truncate(time.Second)
	if at, ok := ctx.Value(authenticationTimeKey{}).(time.Time); ok && !at.IsZero() {
		started = at
	}
	if err := tm.CheckCredentials(ctx, userID, started); err != nil {
		return nil, err
	}

	accessTokenID := uuid.New().String()
	fullName := strings.TrimSpace(firstName + " " + lastName)
	tokenContext := buildTokenContext(email, username, fullName, features, contexts...)

	accessClaims := &Claims{
		AuthenticationTime: started.Unix(),
		UserID:             userID,
		IIN:                strings.TrimSpace(iin),
		Email:              email,
		Username:           username,
		FirstName:          firstName,
		LastName:           lastName,
		TokenType:          AccessToken,
		TokenID:            accessTokenID,

		HasPin:  hasPin,
		Context: tokenContext,

		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tm.cfg.Issuer,
			Audience:  jwt.ClaimStrings{tm.cfg.Audience},
			Subject:   fmt.Sprintf("%d", userID),
			ExpiresAt: jwt.NewNumericDate(now.Add(tm.cfg.AccessTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        accessTokenID,
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(tm.secret)
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshTokenID := uuid.New().String()
	refreshClaims := &Claims{
		AuthenticationTime: started.Unix(),
		UserID:             userID,
		IIN:                strings.TrimSpace(iin),
		Email:              email,
		Username:           username,
		FirstName:          firstName,
		LastName:           lastName,
		TokenType:          RefreshToken,
		TokenID:            refreshTokenID,
		HasPin:             hasPin,
		Context:            cloneTokenContext(tokenContext),

		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tm.cfg.Issuer,
			Audience:  jwt.ClaimStrings{tm.cfg.Audience},
			Subject:   fmt.Sprintf("%d", userID),
			ExpiresAt: jwt.NewNumericDate(now.Add(tm.cfg.RefreshTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        refreshTokenID,
		},
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(tm.secret)
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	// Store tokens in storage for validation/revocation
	if tm.tokenStorage != nil {
		accessExpiresAt := now.Add(tm.cfg.AccessTokenExpiry)
		if err := tm.tokenStorage.StoreToken(ctx, accessTokenID, userID, accessExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to store access token: %w", err)
		}
		if linkedStore, ok := tm.tokenStorage.(LinkedDeviceTokenStorage); ok {
			if err := linkedStore.StoreTokenContext(ctx, accessTokenID, userID, accessExpiresAt, accessClaims.Context); err != nil {
				return nil, fmt.Errorf("failed to store access token context: %w", err)
			}
		}

		refreshExpiresAt := now.Add(tm.cfg.RefreshTokenExpiry)
		if err := tm.tokenStorage.StoreToken(ctx, refreshTokenID, userID, refreshExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to store refresh token: %w", err)
		}
		if linkedStore, ok := tm.tokenStorage.(LinkedDeviceTokenStorage); ok {
			if err := linkedStore.StoreTokenContext(ctx, refreshTokenID, userID, refreshExpiresAt, refreshClaims.Context); err != nil {
				return nil, fmt.Errorf("failed to store refresh token context: %w", err)
			}
		}
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		TokenType:    "Bearer",
		ExpiresIn:    int64(tm.cfg.AccessTokenExpiry.Seconds()),
		ExpiresAt:    now.Add(tm.cfg.AccessTokenExpiry),
	}, nil
}

// ValidateToken validates and parses a token
func (tm *TokenManager) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return tm.secret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaims
	}

	if claims.Issuer != tm.cfg.Issuer {
		return nil, fmt.Errorf("invalid issuer: expected %s, got %s", tm.cfg.Issuer, claims.Issuer)
	}

	validAudience := false
	for _, aud := range claims.Audience {
		if aud == tm.cfg.Audience {
			validAudience = true
			break
		}
	}
	if !validAudience {
		return nil, fmt.Errorf("invalid audience")
	}

	if tm.tokenStorage != nil {
		valid, err := tm.tokenStorage.IsTokenValid(ctx, claims.TokenID)
		if err != nil {
			return nil, fmt.Errorf("failed to check token validity: %w", err)
		}
		if !valid {
			return nil, ErrTokenNotFound
		}
	}

	if err := tm.checkUserActive(ctx, claims.UserID); err != nil {
		return nil, err
	}
	if err := tm.CheckCredentials(ctx, claims.UserID, claims.AuthenticationStartedAt()); err != nil {
		return nil, err
	}
	return claims, nil
}

// RefreshAccessToken generates a new access token from a refresh token
func (tm *TokenManager) RefreshAccessToken(ctx context.Context, refreshTokenString string) (*TokenPair, error) {
	claims, err := tm.ValidateToken(ctx, refreshTokenString)
	if err != nil {
		return nil, err
	}

	if claims.TokenType != RefreshToken {
		return nil, ErrInvalidTokenType
	}

	var features map[string]bool
	if claims.Context != nil {
		features = claims.Context.Features
	}

	return tm.GenerateTokenPair(WithAuthenticationTime(ctx, claims.AuthenticationStartedAt()), claims.UserID, claims.IIN, claims.Email, claims.Username, claims.FirstName, claims.LastName, claims.HasPin, features, claims.Context)
}

func (tm *TokenManager) RevokeToken(ctx context.Context, tokenString string) error {
	claims, err := tm.ValidateToken(ctx, tokenString)
	if err != nil {
		return err
	}

	if tm.tokenStorage == nil {
		return fmt.Errorf("token storage not configured")
	}

	return tm.tokenStorage.RevokeToken(ctx, claims.TokenID)
}

func (tm *TokenManager) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	if tm.tokenStorage == nil {
		return fmt.Errorf("token storage not configured")
	}
	return tm.tokenStorage.RevokeAllUserTokens(ctx, userID)
}

func (tm *TokenManager) RevokeLinkedDeviceTokens(ctx context.Context, userID int64, deviceID string) error {
	if tm.tokenStorage == nil {
		return fmt.Errorf("token storage not configured")
	}

	linkedStore, ok := tm.tokenStorage.(LinkedDeviceTokenStorage)
	if !ok {
		return nil
	}

	deviceID = strings.TrimSpace(deviceID)
	if userID <= 0 || deviceID == "" {
		return nil
	}

	return linkedStore.RevokeTokensByLinkedDevice(ctx, userID, deviceID)
}

func (tm *TokenManager) RevokeOtherWebLinkedTokens(ctx context.Context, userID int64, keepDeviceID string) error {
	if tm.tokenStorage == nil {
		return fmt.Errorf("token storage not configured")
	}

	linkedStore, ok := tm.tokenStorage.(LinkedDeviceTokenStorage)
	if !ok {
		return nil
	}

	keepDeviceID = strings.TrimSpace(keepDeviceID)
	if userID <= 0 || keepDeviceID == "" {
		return nil
	}

	return linkedStore.RevokeWebLinkedTokensExceptDevice(ctx, userID, keepDeviceID)
}

// ExtractClaims extracts claims from token without full validation (for expired tokens)
func (tm *TokenManager) ExtractClaims(tokenString string) (*Claims, error) {
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidClaims
	}
	return claims, nil
}

func buildTokenContext(email, username, fullName string, features map[string]bool, contexts ...*Context) *Context {
	tokenContext := &Context{
		User: &ContextUser{
			Name:     fullName,
			Email:    email,
			Username: username,
		},
		Features: cloneFeatureMap(features),
	}

	if len(contexts) == 0 || contexts[0] == nil {
		return tokenContext
	}

	source := contexts[0]
	if tokenContext.Features == nil {
		tokenContext.Features = make(map[string]bool)
	}
	for key, value := range source.Features {
		if _, exists := tokenContext.Features[key]; !exists {
			tokenContext.Features[key] = value
		}
	}
	tokenContext.LinkedDeviceID = strings.TrimSpace(source.LinkedDeviceID)
	return tokenContext
}

func cloneTokenContext(source *Context) *Context {
	if source == nil {
		return nil
	}

	cloned := &Context{
		LinkedDeviceID: strings.TrimSpace(source.LinkedDeviceID),
	}
	if source.User != nil {
		cloned.User = &ContextUser{
			Name:     source.User.Name,
			Email:    source.User.Email,
			Username: source.User.Username,
		}
	}
	cloned.Features = cloneFeatureMap(source.Features)
	return cloned
}

func cloneFeatureMap(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}

	cloned := make(map[string]bool, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

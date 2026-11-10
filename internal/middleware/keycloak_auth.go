package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type UserRepo interface {
	GetByKeycloakID(ctx context.Context, kcID string) (*domain.User, error)
	UpsertFromKeycloak(ctx context.Context, u *domain.User) error
	UpdateLastLogin(ctx context.Context, userID int64, loginAt time.Time) error
}

type KeycloakAuthMiddleware struct {
	verifier     *keycloak.Verifier
	userRepo     UserRepo
	logger       *zap.Logger
	tokenManager *auth.TokenManager
}

func NewKeycloakAuthMiddleware(verifier *keycloak.Verifier, userRepo UserRepo, logger *zap.Logger, tokenManagers ...*auth.TokenManager) *KeycloakAuthMiddleware {
	mw := &KeycloakAuthMiddleware{verifier: verifier, userRepo: userRepo, logger: logger}
	if len(tokenManagers) > 0 {
		mw.tokenManager = tokenManagers[0]
	}
	return mw
}

func (m *KeycloakAuthMiddleware) SetTokenManager(tokenManager *auth.TokenManager) {
	m.tokenManager = tokenManager
}

func (m *KeycloakAuthMiddleware) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractBearerTokenKC(c)
		if tokenStr == "" {
			m.logger.Warn("Missing authentication token",
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
			)
			m.respondError(c, apperrors.Unauthorized("Missing authentication token"))
			c.Abort()
			return
		}

		// Local HS256 tokens are the common web/mobile path. Validate them before
		// invoking the Keycloak RS256 verifier so they never touch the JWKS path.
		if m.tokenManager != nil {
			if jwtClaims, jwtErr := m.tokenManager.ValidateToken(c.Request.Context(), tokenStr); jwtErr == nil {
				if jwtClaims.TokenType != auth.AccessToken {
					m.respondError(c, apperrors.TokenInvalid().WithDetail("reason", "not an access token"))
					c.Abort()
					return
				}
				m.applyLocalJWTClaims(c, jwtClaims)
				c.Next()
				return
			} else if errors.Is(jwtErr, auth.ErrCredentialsRevoked) {
				m.respondError(c, apperrors.ReauthRequired("Please sign in again"))
				c.Abort()
				return
			} else if errors.Is(jwtErr, auth.ErrUserInactive) {
				m.respondError(c, apperrors.Forbidden("account inactive"))
				c.Abort()
				return
			} else if errors.Is(jwtErr, auth.ErrUserAccessLookup) {
				m.respondError(c, apperrors.Internal("account access unavailable"))
				c.Abort()
				return
			}
		}

		_, claims, err := m.verifier.Parse(c.Request.Context(), tokenStr)
		if err != nil {
			iss, sub, azp, audiences := extractJWTDebugSummary(tokenStr)
			m.logger.Warn("Token validation failed",
				zap.Error(err),
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
				zap.String("issuer", iss),
				zap.String("subject", sub),
				zap.String("authorized_party", azp),
				zap.Strings("audiences", audiences),
				zap.Strings("trusted_audiences", m.verifier.TrustedAudiences()),
				zap.Strings("allowed_azp", m.verifier.AllowedAZP()),
				zap.String("result", "rejected"),
			)
			m.respondError(c, classifyTokenValidationError(err))
			c.Abort()
			return
		}

		audiences := keycloak.ExtractAudiencesForLog(claims["aud"])
		iss, _ := claims["iss"].(string)
		sub, _ := claims["sub"].(string)
		azp, _ := claims["azp"].(string)
		m.logger.Debug("Token validation completed",
			zap.String("path", c.Request.URL.Path),
			zap.String("method", c.Request.Method),
			zap.String("issuer", iss),
			zap.String("subject", sub),
			zap.String("authorized_party", azp),
			zap.Strings("audiences", audiences),
			zap.Strings("trusted_audiences", m.verifier.TrustedAudiences()),
			zap.Strings("allowed_azp", m.verifier.AllowedAZP()),
			zap.String("result", "accepted"),
		)

		kcID, _ := claims["sub"].(string)
		if kcID == "" {
			m.respondError(c, apperrors.TokenInvalid().WithDetail("reason", "missing sub"))
			c.Abort()
			return
		}

		if m.tokenManager != nil {
			existing, lookupErr := m.userRepo.GetByKeycloakID(c.Request.Context(), kcID)
			if lookupErr == nil && existing != nil {
				if err := m.tokenManager.CheckCredentialClaims(c.Request.Context(), existing.ID, claims); err != nil {
					m.respondError(c, apperrors.ReauthRequired("Please sign in again"))
					c.Abort()
					return
				}
			}
		}
		email, _ := claims["email"].(string)
		email = strings.ToLower(email)
		username, _ := claims["preferred_username"].(string)
		firstName, _ := claims["given_name"].(string)
		lastName, _ := claims["family_name"].(string)

		// iin может отсутствовать -> nil
		iin, _ := claims["iin"].(string)

		roles := extractRealmRoles(claims)
		role := mapKeycloakRolesToDomain(roles)

		// pointers for nullable fields
		kcIDCopy := kcID

		var iinPtr *string
		if strings.TrimSpace(iin) != "" {
			iinCopy := iin
			iinPtr = &iinCopy
		}

		u := &domain.User{
			KeycloakID: &kcIDCopy,
			IIN:        iinPtr,
			Email:      email,
			Username:   username,
			FirstName:  firstName,
			LastName:   lastName,
			IsActive:   true,
			Role:       role,
		}

		// sync local user
		if err := m.userRepo.UpsertFromKeycloak(c.Request.Context(), u); err != nil {
			m.logger.Warn("upsert user failed", zap.Error(err))
			if errors.Is(err, repository.ErrKeycloakUserNotLinked) {
				m.respondError(c, apperrors.Forbidden("Keycloak user is not linked to local user. Please link by keycloak_id, iin, email or username.").
					WithDetail("reason", "KEYCLOAK_USER_NOT_LINKED"))
				c.Abort()
				return
			}
			m.respondError(c, apperrors.ToAppError(err))
			c.Abort()
			return
		}

		if u.ID <= 0 {
			m.logger.Error("user id not set after upsert", zap.String("kc_id", kcID))
			m.respondError(c, apperrors.Internal("failed to resolve user id"))
			c.Abort()
			return
		}

		if lookup, ok := m.userRepo.(interface {
			IsUserActive(context.Context, int64) (bool, error)
		}); ok {
			active, err := lookup.IsUserActive(c.Request.Context(), u.ID)
			if err != nil {
				m.respondError(c, apperrors.Internal("account access unavailable"))
				c.Abort()
				return
			}
			if !active {
				m.respondError(c, apperrors.Forbidden("account inactive"))
				c.Abort()
				return
			}
		}
		if m.tokenManager != nil {
			if err := m.tokenManager.CheckCredentialClaims(c.Request.Context(), u.ID, claims); err != nil {
				m.respondError(c, apperrors.ReauthRequired("Please sign in again"))
				c.Abort()
				return
			}
		}
		_ = m.userRepo.UpdateLastLogin(c.Request.Context(), u.ID, time.Now())

		// set gin context
		c.Set("user_id", u.ID)
		c.Set("keycloak_id", kcID) // здесь можно оставить string, для удобства
		c.Set("azp", azp)
		c.Set("email", email)
		c.Set("username", username)
		c.Set("roles", roles)
		c.Set("role", role)

		// Create compat claims for handlers relying on middleware.GetClaims
		authClaims := &auth.Claims{
			UserID:    u.ID,
			IIN:       iin,
			Email:     email,
			Username:  username,
			FirstName: firstName,
			LastName:  lastName,
			TokenType: auth.AccessToken,
		}
		c.Set("claims", authClaims)

		// set request context
		ctx := c.Request.Context()
		if at, err := auth.AuthenticationTime(claims); err == nil {
			ctx = auth.WithAuthenticationTime(ctx, at)
		}
		ctx = context.WithValue(ctx, UserIDKey, u.ID)
		ctx = context.WithValue(ctx, UserEmailKey, email)
		ctx = context.WithValue(ctx, ClaimsKey, authClaims)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

func (m *KeycloakAuthMiddleware) applyLocalJWTClaims(c *gin.Context, claims *auth.Claims) {
	c.Set("user_id", claims.UserID)
	c.Set("email", claims.Email)
	c.Set("username", claims.Username)
	c.Set("claims", claims)

	ctx := c.Request.Context()
	ctx = auth.WithAuthenticationTime(ctx, claims.AuthenticationStartedAt())
	ctx = context.WithValue(ctx, UserIDKey, claims.UserID)
	ctx = context.WithValue(ctx, UserEmailKey, claims.Email)
	ctx = context.WithValue(ctx, ClaimsKey, claims)
	c.Request = c.Request.WithContext(ctx)
}

func extractRealmRoles(claims map[string]any) []string {
	ra, ok := claims["realm_access"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := ra["roles"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mapKeycloakRolesToDomain(roles []string) domain.Role {
	has := func(r string) bool {
		for _, x := range roles {
			if x == r {
				return true
			}
		}
		return false
	}

	switch {
	case has("admin") || has("external_user_admin"):
		return domain.RoleSuperAdmin
	case has("manager") || has("leader"):
		return domain.RoleManager
	default:
		return domain.RoleUser
	}
}

func (m *KeycloakAuthMiddleware) respondError(c *gin.Context, err *apperrors.AppError) {
	RespondAppError(c, err)
}

func classifyTokenValidationError(err error) *apperrors.AppError {
	if err == nil {
		return apperrors.TokenInvalid()
	}

	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, keycloak.ErrInvalidIssuer):
		return apperrors.TokenInvalid().WithDetail("reason", "invalid issuer")
	case errors.Is(err, keycloak.ErrMissingAudience):
		return apperrors.TokenInvalid().WithDetail("reason", "missing audience")
	case errors.Is(err, keycloak.ErrInvalidAudience):
		return apperrors.TokenInvalid().WithDetail("reason", "invalid audience")
	case errors.Is(err, keycloak.ErrInvalidAuthorizedParty):
		return apperrors.TokenInvalid().WithDetail("reason", "invalid authorized party")
	case errors.Is(err, keycloak.ErrRefreshTokenNotAccepted):
		return apperrors.TokenInvalid().WithDetail("reason", "refresh token is not accepted")
	}
	if strings.Contains(msg, "expired") {
		return apperrors.TokenExpired()
	}
	if strings.Contains(msg, "signature") || strings.Contains(msg, "signing method") || strings.Contains(msg, "unknown kid") {
		return apperrors.TokenInvalid().WithDetail("reason", "invalid token signature")
	}

	return apperrors.TokenInvalid()
}

func extractJWTDebugSummary(tokenStr string) (iss, sub, azp string, audiences []string) {
	parser := jwt.Parser{}
	claims := jwt.MapClaims{}
	if _, _, err := parser.ParseUnverified(tokenStr, claims); err != nil {
		return "", "", "", nil
	}
	iss, _ = claims["iss"].(string)
	sub, _ = claims["sub"].(string)
	azp, _ = claims["azp"].(string)
	return iss, sub, azp, keycloak.ExtractAudiencesForLog(claims["aud"])
}

func extractBearerTokenKC(c *gin.Context) string {
	// 1. Check Authorization header
	h := c.GetHeader("Authorization")
	if h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	// 2. Check query parameters (for WebSockets)
	// Standard practice is often ?token=... or ?access_token=...
	if token := c.Query("token"); token != "" {
		return token
	}
	if token := c.Query("access_token"); token != "" {
		return token
	}

	if token, err := c.Cookie("access_token"); err == nil && token != "" {
		return token
	}

	return ""
}

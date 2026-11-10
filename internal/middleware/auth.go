package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"go.uber.org/zap"
)

type contextKey string

const (
	UserIDKey    contextKey = "user_id"
	UserEmailKey contextKey = "user_email"
	ClaimsKey    contextKey = "claims"
)

// AuthMiddleware handles JWT authentication
type AuthMiddleware struct {
	tokenManager *auth.TokenManager
	logger       *zap.Logger
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(tokenManager *auth.TokenManager, logger *zap.Logger) *AuthMiddleware {
	return &AuthMiddleware{
		tokenManager: tokenManager,
		logger:       logger,
	}
}

// Authenticate validates JWT token and sets user context
func (m *AuthMiddleware) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ExtractToken(c)
		if token == "" {
			m.respondError(c, apperrors.Unauthorized("Missing authentication token"))
			c.Abort()
			return
		}

		claims, err := m.tokenManager.ValidateToken(c.Request.Context(), token)
		if err != nil {
			m.logger.Warn("Token validation failed",
				zap.Error(err),
				zap.String("path", c.Request.URL.Path),
			)

			if err == auth.ErrExpiredToken {
				m.respondError(c, apperrors.TokenExpired())
			} else if errors.Is(err, auth.ErrUserInactive) {
				m.respondError(c, apperrors.Forbidden("account inactive"))
			} else if errors.Is(err, auth.ErrUserAccessLookup) {
				m.respondError(c, apperrors.Internal("account access unavailable"))
			} else {
				m.respondError(c, apperrors.TokenInvalid())
			}
			c.Abort()
			return
		}

		// Ensure it's an access token
		if claims.TokenType != auth.AccessToken {
			m.respondError(c, apperrors.TokenInvalid().WithDetail("reason", "not an access token"))
			c.Abort()
			return
		}

		// Set user context
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, UserEmailKey, claims.Email)
		ctx = context.WithValue(ctx, ClaimsKey, claims)
		c.Request = c.Request.WithContext(ctx)

		// Also set in gin context for convenience
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("username", claims.Username)
		c.Set("claims", claims)

		m.logger.Debug("User authenticated",
			zap.Int64("user_id", claims.UserID),
			zap.String("email", claims.Email),
			zap.String("path", c.Request.URL.Path),
		)

		c.Next()
	}
}

// Optional validates JWT token if present but doesn't require it
func (m *AuthMiddleware) Optional() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ExtractToken(c)
		if token == "" {
			c.Next()
			return
		}

		claims, err := m.tokenManager.ValidateToken(c.Request.Context(), token)
		if err != nil {
			// Log but don't block
			m.logger.Debug("Optional auth failed", zap.Error(err))
			c.Next()
			return
		}

		// Set user context
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, UserEmailKey, claims.Email)
		ctx = context.WithValue(ctx, ClaimsKey, claims)
		c.Request = c.Request.WithContext(ctx)

		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("username", claims.Username)
		c.Set("claims", claims)

		c.Next()
	}
}

// ExtractToken extracts JWT token from request
func ExtractToken(c *gin.Context) string {
	// Check Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	// Check query parameter (for WebSocket)
	if token := c.Query("token"); token != "" {
		return token
	}
	if token := c.Query("access_token"); token != "" {
		return token
	}

	// Check cookie
	if token, err := c.Cookie("access_token"); err == nil && token != "" {
		return token
	}

	return ""
}

// GetUserID gets user ID from context
func GetUserID(c *gin.Context) (int64, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}

	id, ok := userID.(int64)
	return id, ok
}

// MustGetUserID gets user ID from context or panics
func MustGetUserID(c *gin.Context) int64 {
	userID, ok := GetUserID(c)
	if !ok {
		panic("user_id not found in context")
	}
	return userID
}

// GetClaims gets JWT claims from context
func GetClaims(c *gin.Context) (*auth.Claims, bool) {
	claims, exists := c.Get("claims")
	if !exists {
		return nil, false
	}

	claimsTyped, ok := claims.(*auth.Claims)
	return claimsTyped, ok
}

// respondError sends error response
func (m *AuthMiddleware) respondError(c *gin.Context, err *apperrors.AppError) {
	RespondAppError(c, err)
}

// RequireRefreshToken validates refresh token
func (m *AuthMiddleware) RequireRefreshToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ExtractToken(c)
		if token == "" {
			m.respondError(c, apperrors.Unauthorized("Missing refresh token"))
			c.Abort()
			return
		}

		claims, err := m.tokenManager.ValidateToken(c.Request.Context(), token)
		if err != nil {
			if err == auth.ErrExpiredToken {
				m.respondError(c, apperrors.TokenExpired())
			} else {
				m.respondError(c, apperrors.TokenInvalid())
			}
			c.Abort()
			return
		}

		// Ensure it's a refresh token
		if claims.TokenType != auth.RefreshToken {
			m.respondError(c, apperrors.TokenInvalid().WithDetail("reason", "not a refresh token"))
			c.Abort()
			return
		}

		c.Set("refresh_token", token)
		c.Set("claims", claims)
		c.Next()
	}
}

// ExtractRefreshToken extracts token and sets it in context without local validation (for Keycloak)
func (m *AuthMiddleware) ExtractRefreshToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ExtractToken(c)
		if token == "" {
			m.respondError(c, apperrors.Unauthorized("Missing refresh token"))
			c.Abort()
			return
		}

		c.Set("refresh_token", token)
		c.Next()
	}
}

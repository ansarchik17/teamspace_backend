package middleware

import (
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

const InternalTokenHeader = "X-Internal-Token"

func InternalTokenAuth(expectedToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		expectedToken = strings.TrimSpace(expectedToken)
		if expectedToken == "" {
			RespondAppError(c, apperrors.Forbidden("internal notification token is not configured"))
			c.Abort()
			return
		}

		actualToken := strings.TrimSpace(c.GetHeader(InternalTokenHeader))
		if actualToken == "" {
			authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
				actualToken = strings.TrimSpace(authHeader[len("Bearer "):])
			}
		}
		if actualToken == "" || subtle.ConstantTimeCompare([]byte(actualToken), []byte(expectedToken)) != 1 {
			RespondAppError(c, apperrors.Unauthorized("invalid internal token"))
			c.Abort()
			return
		}

		c.Next()
	}
}

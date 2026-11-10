package middleware

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/telemetry"
	"go.uber.org/zap"
)

// APITracking middleware records API request telemetry for monitoring third-party requests without blocking
func APITracking(tracker *telemetry.APITracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		if tracker == nil {
			return
		}

		// Ignore static or internal admin routes from telemetry noise
		if strings.HasPrefix(path, "/api/admin/monitoring") || strings.HasPrefix(path, "/swagger") {
			return
		}

		duration := time.Since(start)
		appClientHeader := c.Request.Header.Get("X-App-Client")
		origin := c.Request.Header.Get("Origin")
		referer := c.Request.Header.Get("Referer")
		userAgent := c.Request.UserAgent()

		isThirdParty, clientType := tracker.ClassifyClient(appClientHeader, origin, referer, userAgent)

		tracker.Record(telemetry.RequestLog{
			ID:           c.GetString("request_id"),
			Timestamp:    start,
			Method:       c.Request.Method,
			Path:         path,
			IP:           c.ClientIP(),
			UserAgent:    userAgent,
			Origin:       origin,
			Referer:      referer,
			StatusCode:   c.Writer.Status(),
			DurationMs:   duration.Milliseconds(),
			IsThirdParty: isThirdParty,
			ClientType:   clientType,
		})
	}
}

// Logger creates a logging middleware
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		duration := time.Since(start)
		statusCode := c.Writer.Status()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Int("status", statusCode),
			zap.Duration("duration", duration),
			zap.String("ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			config.LogOp(config.LogOpFromMethod(c.Request.Method)),
		}

		if requestID, exists := c.Get("request_id"); exists {
			if id, ok := requestID.(string); ok && id != "" {
				fields = append(fields, zap.String("request_id", id))
			}
		}

		// Add user ID if available
		if userID, exists := c.Get("user_id"); exists {
			if id, ok := userID.(int64); ok {
				fields = append(fields, zap.Int64("user_id", id))
			}
		}

		// Add error if exists
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("error", c.Errors.String()))
		}
		if errorCode, exists := c.Get("error_code"); exists {
			if code, ok := errorCode.(string); ok && code != "" {
				fields = append(fields, zap.String("error_code", code))
			}
		}
		if errorMessage, exists := c.Get("error_message"); exists {
			if message, ok := errorMessage.(string); ok && message != "" {
				fields = append(fields, zap.String("error_message", message))
			}
		}

		// Log with appropriate level based on status code
		msg := fmt.Sprintf(
			"%s %s %s",
			config.ColorizeHTTPStatus(statusCode),
			config.LogEvent(config.LogOpFromMethod(c.Request.Method), c.Request.Method),
			path,
		)
		switch {
		case statusCode >= 500:
			logger.Error(msg, fields...)
		case statusCode >= 400:
			logger.Warn(msg, fields...)
		case statusCode >= 300:
			logger.Info(msg, fields...)
		default:
			logger.Info(msg, fields...)
		}
	}
}

// Recovery creates a recovery middleware
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("Panic recovered",
					zap.Any("error", err),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
				)

				RespondAppError(c, apperrors.Internal("Internal server error"))
				c.Abort()
			}
		}()

		c.Next()
	}
}

// CORS creates a CORS middleware
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		c.Header("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")

		if origin != "" && corsOriginAllowed(cfg.AllowedOrigins, origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		} else if origin == "" && corsAllowsWildcard(cfg.AllowedOrigins) {
			c.Header("Access-Control-Allow-Origin", "*")
		}

		allowedHeaders := strings.Join(cfg.AllowedHeaders, ", ")
		if requestedHeaders := strings.TrimSpace(c.GetHeader("Access-Control-Request-Headers")); requestedHeaders != "" {
			allowedHeaders = requestedHeaders
		}
		c.Header("Access-Control-Allow-Headers", allowedHeaders)
		c.Header("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
		c.Header("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func corsOriginAllowed(allowedOrigins []string, origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return len(allowedOrigins) == 0
}

func corsAllowsWildcard(allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		if strings.TrimSpace(allowed) == "*" {
			return true
		}
	}
	return len(allowedOrigins) == 0
}

// RequestID adds a unique request ID to each request
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		c.Header("X-Request-ID", requestID)
		c.Set("request_id", requestID)

		c.Next()
	}
}

// Timeout adds timeout to requests
func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Skip timeout for WebSocket connections
		if c.Request.Header.Get("Upgrade") == "websocket" {
			c.Next()
			return
		}

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// SecureHeaders adds security headers
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

func RecordAppError(c *gin.Context, err *apperrors.AppError) {
	if c == nil || err == nil {
		return
	}
	c.Set("error_code", string(err.Code))
	c.Set("error_message", err.Message)
	_ = c.Error(err)
}

func RespondAppError(c *gin.Context, err *apperrors.AppError) {
	if err == nil {
		err = apperrors.Internal("An unexpected error occurred")
	}
	RecordAppError(c, err)
	c.JSON(err.StatusCode, apperrors.ResponseBodyLocale(err, RequestLocale(c)))
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), uuid.New().String()[:8])
}

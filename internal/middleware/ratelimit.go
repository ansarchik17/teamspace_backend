package middleware

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

// RateLimiter implements rate limiting
type RateLimiter struct {
	redis      *redis.Client
	logger     *zap.Logger
	maxReqs    int
	window     time.Duration
	enabled    bool
	localCache *localRateLimitCache // Fallback when Redis is unavailable
}

// localRateLimitCache is a simple in-memory cache for rate limiting
type localRateLimitCache struct {
	mu      sync.RWMutex
	cache   map[string]*rateLimitEntry
	cleanup time.Duration
}

type rateLimitEntry struct {
	count     int
	expiresAt time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(redis *redis.Client, logger *zap.Logger, maxReqs int, window time.Duration, enabled bool) *RateLimiter {
	rl := &RateLimiter{
		redis:   redis,
		logger:  logger,
		maxReqs: maxReqs,
		window:  window,
		enabled: enabled,
		localCache: &localRateLimitCache{
			cache:   make(map[string]*rateLimitEntry),
			cleanup: window,
		},
	}

	// Start cleanup goroutine for local cache
	// Start cleanup goroutine for local cache
	if window > 0 {
		go rl.localCache.startCleanup()
	}

	return rl
}

// Limit applies rate limiting
func (rl *RateLimiter) Limit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.enabled {
			c.Next()
			return
		}

		identifier := rl.getIdentifier(c)

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		method := c.Request.Method

		allowed, remaining, resetAt, err := rl.checkLimit(
			c.Request.Context(),
			identifier,
			route,
			method,
		)

		// Если Redis/лимитер упал — логируем и пропускаем запрос (fail-open)
		if err != nil {
			rl.logger.Error("Rate limit check failed", zap.Error(err))
			c.Next()
			return
		}

		// Set rate limit headers
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", rl.maxReqs))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))

		if !allowed {
			retryAfter := int(time.Until(resetAt).Seconds())
			if retryAfter < 0 {
				retryAfter = 0
			}
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))

			respondError(c, apperrors.TooManyRequests("Rate limit exceeded"))
			c.Abort()
			return
		}

		c.Next()
	}
}

// checkLimit checks if request is within rate limit
func (rl *RateLimiter) checkLimit(
	ctx context.Context,
	identifier string,
	route string,
	method string,
) (allowed bool, remaining int, resetAt time.Time, err error) {

	// Ключ на пользователя/айпи + эндпоинт (метод + route)
	key := fmt.Sprintf("ratelimit:%s:%s:%s", identifier, method, route)

	// Try Redis first
	if rl.redis != nil {
		return rl.checkRedisLimit(ctx, key)
	}

	// Fallback to local cache
	return rl.checkLocalLimit(key)
}

// checkRedisLimit checks rate limit using Redis
func (rl *RateLimiter) checkRedisLimit(ctx context.Context, key string) (bool, int, time.Time, error) {
	now := time.Now()
	windowStart := now.Add(-rl.window)

	pipe := rl.redis.Pipeline()

	// Remove old entries
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart.UnixNano()))

	// Count current requests
	zcard := pipe.ZCard(ctx, key)

	// Add current request
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: fmt.Sprintf("%d", now.UnixNano()),
	})

	// Set expiry
	pipe.Expire(ctx, key, rl.window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, 0, time.Time{}, err
	}

	count := int(zcard.Val())

	allowed := count < rl.maxReqs

	remaining := rl.maxReqs - count - 1
	if remaining < 0 {
		remaining = 0
	}

	// Простой resetAt (не идеально, но ок для начала)
	resetAt := now.Add(rl.window)

	return allowed, remaining, resetAt, nil
}

// checkLocalLimit checks rate limit using local cache
func (rl *RateLimiter) checkLocalLimit(key string) (bool, int, time.Time, error) {
	rl.localCache.mu.Lock()
	defer rl.localCache.mu.Unlock()

	now := time.Now()
	entry, exists := rl.localCache.cache[key]

	if !exists || entry.expiresAt.Before(now) {
		rl.localCache.cache[key] = &rateLimitEntry{
			count:     1,
			expiresAt: now.Add(rl.window),
		}
		return true, rl.maxReqs - 1, now.Add(rl.window), nil
	}

	if entry.count >= rl.maxReqs {
		return false, 0, entry.expiresAt, nil
	}

	entry.count++
	remaining := rl.maxReqs - entry.count
	if remaining < 0 {
		remaining = 0
	}

	return true, remaining, entry.expiresAt, nil
}

// getIdentifier gets the identifier for rate limiting
func (rl *RateLimiter) getIdentifier(c *gin.Context) string {
	if userID, exists := c.Get("user_id"); exists {
		if id, ok := userID.(int64); ok {
			return fmt.Sprintf("user:%d", id)
		}
	}
	return fmt.Sprintf("ip:%s", c.ClientIP())
}

// startCleanup periodically cleans up expired entries
func (lc *localRateLimitCache) startCleanup() {
	ticker := time.NewTicker(lc.cleanup)
	defer ticker.Stop()

	for range ticker.C {
		lc.mu.Lock()
		now := time.Now()
		for key, entry := range lc.cache {
			if entry.expiresAt.Before(now) {
				delete(lc.cache, key)
			}
		}
		lc.mu.Unlock()
	}
}

// respondError sends error response
func respondError(c *gin.Context, err *apperrors.AppError) {
	RespondAppError(c, err)
}

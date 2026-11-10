package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAILimiterConfig(t *testing.T) {
	valid := AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 2,
		RateLimitRequests:    20,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             45 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		FailClosed:           true,
	}
	require.NoError(t, valid.Validate())

	for _, mutate := range []func(*AILimiterConfig){
		func(c *AILimiterConfig) { c.MaxConcurrentPerUser = 0 },
		func(c *AILimiterConfig) { c.MaxConcurrentPerUser = 1001 },
		func(c *AILimiterConfig) { c.RateLimitRequests = 0 },
		func(c *AILimiterConfig) { c.RateLimitWindow = 0 },
		func(c *AILimiterConfig) { c.HeartbeatInterval = 0 },
		func(c *AILimiterConfig) { c.LeaseTTL = c.HeartbeatInterval },
		func(c *AILimiterConfig) { c.LeaseTTL = 10 * time.Second },
	} {
		bad := valid
		mutate(&bad)
		require.Error(t, bad.Validate())
	}

	t.Setenv("AI_LIMITER_ENABLED", "true")
	t.Setenv("AI_LIMITER_MAX_CONCURRENT", "3")
	t.Setenv("AI_LIMITER_RATE_REQUESTS", "30")
	t.Setenv("AI_LIMITER_RATE_WINDOW", "2m")
	t.Setenv("AI_LIMITER_LEASE_TTL", "60s")
	t.Setenv("AI_LIMITER_HEARTBEAT_INTERVAL", "20s")
	t.Setenv("AI_LIMITER_FAIL_CLOSED", "false")

	loaded := loadAILimiterConfig()
	require.True(t, loaded.Enabled)
	require.Equal(t, 3, loaded.MaxConcurrentPerUser)
	require.Equal(t, 30, loaded.RateLimitRequests)
	require.Equal(t, 2*time.Minute, loaded.RateLimitWindow)
	require.Equal(t, 60*time.Second, loaded.LeaseTTL)
	require.Equal(t, 20*time.Second, loaded.HeartbeatInterval)
	require.False(t, loaded.FailClosed)
	require.NoError(t, loaded.Validate())
}

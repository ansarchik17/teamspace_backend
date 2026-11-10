package config

import (
	"fmt"
	"time"
)

type AILimiterConfig struct {
	Enabled              bool
	MaxConcurrentPerUser int
	RateLimitRequests    int
	RateLimitWindow      time.Duration
	LeaseTTL             time.Duration
	HeartbeatInterval    time.Duration
	FailClosed           bool
}

func loadAILimiterConfig() AILimiterConfig {
	return AILimiterConfig{
		Enabled:              getEnvAsBool("AI_LIMITER_ENABLED", true),
		MaxConcurrentPerUser: getEnvAsInt("AI_LIMITER_MAX_CONCURRENT", 2),
		RateLimitRequests:    getEnvAsInt("AI_LIMITER_RATE_REQUESTS", 20),
		RateLimitWindow:      getDuration("AI_LIMITER_RATE_WINDOW", time.Minute),
		LeaseTTL:             getDuration("AI_LIMITER_LEASE_TTL", 45*time.Second),
		HeartbeatInterval:    getDuration("AI_LIMITER_HEARTBEAT_INTERVAL", 15*time.Second),
		FailClosed:           getEnvAsBool("AI_LIMITER_FAIL_CLOSED", true),
	}
}

func (c AILimiterConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.MaxConcurrentPerUser < 1 || c.MaxConcurrentPerUser > 1000 {
		return fmt.Errorf("AI_LIMITER_MAX_CONCURRENT must be between 1 and 1000")
	}
	if c.RateLimitRequests < 1 || c.RateLimitRequests > 100000 {
		return fmt.Errorf("AI_LIMITER_RATE_REQUESTS must be between 1 and 100000")
	}
	if c.RateLimitWindow < time.Second || c.RateLimitWindow > 24*time.Hour {
		return fmt.Errorf("AI_LIMITER_RATE_WINDOW must be between 1s and 24h")
	}
	if c.HeartbeatInterval < 100*time.Millisecond || c.HeartbeatInterval > 10*time.Minute {
		return fmt.Errorf("AI_LIMITER_HEARTBEAT_INTERVAL must be between 100ms and 10m")
	}
	if c.LeaseTTL <= c.HeartbeatInterval || c.LeaseTTL > 30*time.Minute {
		return fmt.Errorf("AI_LIMITER_LEASE_TTL must be strictly greater than HeartbeatInterval and <= 30m")
	}
	return nil
}

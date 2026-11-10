package config

import (
	"fmt"
	"time"
)

type AIReconciliationConfig struct {
	Enabled       bool
	Interval      time.Duration
	StaleAfter    time.Duration
	LeaseDuration time.Duration
	LookupTimeout time.Duration
	RetryDelay    time.Duration
	BatchSize     int
	MaxClaims     int
}

func loadAIReconciliationConfig() AIReconciliationConfig {
	return AIReconciliationConfig{Enabled: getEnvAsBool("AI_RECONCILIATION_ENABLED", false), Interval: getDuration("AI_RECONCILIATION_INTERVAL", time.Minute), StaleAfter: getDuration("AI_RECONCILIATION_STALE_AFTER", 15*time.Minute), LeaseDuration: getDuration("AI_RECONCILIATION_LEASE", 2*time.Minute), LookupTimeout: getDuration("AI_RECONCILIATION_LOOKUP_TIMEOUT", 30*time.Second), RetryDelay: getDuration("AI_RECONCILIATION_RETRY_DELAY", 5*time.Minute), BatchSize: getEnvAsInt("AI_RECONCILIATION_BATCH_SIZE", 25), MaxClaims: getEnvAsInt("AI_RECONCILIATION_MAX_CLAIMS", 10)}
}

func (c AIReconciliationConfig) Validate() error {
	if c.Interval < time.Second || c.Interval > time.Hour || c.StaleAfter < time.Second || c.StaleAfter > 30*24*time.Hour || c.LookupTimeout < time.Second || c.LookupTimeout > 10*time.Minute || c.LeaseDuration <= c.LookupTimeout+10*time.Second || c.LeaseDuration > time.Hour || c.RetryDelay < time.Second || c.RetryDelay > 24*time.Hour || c.BatchSize < 1 || c.BatchSize > 1000 || c.MaxClaims < 1 || c.MaxClaims > 1000 {
		return fmt.Errorf("invalid AI reconciliation timing or batch configuration")
	}
	return nil
}

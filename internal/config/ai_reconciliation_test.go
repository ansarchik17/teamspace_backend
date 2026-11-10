package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAIReconciliationConfig(t *testing.T) {
	valid := AIReconciliationConfig{Enabled: true, Interval: time.Minute, StaleAfter: 15 * time.Minute, LeaseDuration: 2 * time.Minute, LookupTimeout: 30 * time.Second, RetryDelay: 5 * time.Minute, BatchSize: 25, MaxClaims: 10}
	require.NoError(t, valid.Validate())
	for _, mutate := range []func(*AIReconciliationConfig){
		func(c *AIReconciliationConfig) { c.LeaseDuration = c.LookupTimeout },
		func(c *AIReconciliationConfig) { c.Interval = 0 },
		func(c *AIReconciliationConfig) { c.StaleAfter = -time.Second },
		func(c *AIReconciliationConfig) { c.BatchSize = 1001 },
		func(c *AIReconciliationConfig) { c.MaxClaims = 0 },
	} {
		bad := valid
		mutate(&bad)
		require.Error(t, bad.Validate())
	}
	t.Setenv("AI_RECONCILIATION_ENABLED", "false")
	t.Setenv("AI_RECONCILIATION_INTERVAL", "1m")
	t.Setenv("AI_RECONCILIATION_STALE_AFTER", "15m")
	t.Setenv("AI_RECONCILIATION_LEASE", "2m")
	t.Setenv("AI_RECONCILIATION_LOOKUP_TIMEOUT", "30s")
	t.Setenv("AI_RECONCILIATION_RETRY_DELAY", "5m")
	t.Setenv("AI_RECONCILIATION_BATCH_SIZE", "25")
	t.Setenv("AI_RECONCILIATION_MAX_CLAIMS", "10")
	cfg := loadAIReconciliationConfig()
	require.False(t, cfg.Enabled)
	require.NoError(t, cfg.Validate())
}

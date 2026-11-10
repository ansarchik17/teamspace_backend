package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIAccountingConfig_Defaults(t *testing.T) {
	t.Setenv("AI_ACCOUNTING_ENABLED", "true")
	t.Setenv("AI_ENFORCEMENT_MODE", "enforced")

	cfg := loadAIAccountingConfig()
	require.True(t, cfg.Enabled)
	require.Equal(t, domain.AIEnforcementModeEnforced, cfg.EnforcementMode)
	require.True(t, cfg.IsEnforced())
	require.False(t, cfg.IsObserve())
	require.NoError(t, cfg.Validate())
}

func TestAIAccountingConfig_ObserveMode(t *testing.T) {
	t.Setenv("AI_ACCOUNTING_ENABLED", "true")
	t.Setenv("AI_ENFORCEMENT_MODE", "observe")

	cfg := loadAIAccountingConfig()
	require.True(t, cfg.Enabled)
	require.Equal(t, domain.AIEnforcementModeObserve, cfg.EnforcementMode)
	require.False(t, cfg.IsEnforced())
	require.True(t, cfg.IsObserve())
	require.NoError(t, cfg.Validate())
}

func TestAIAccountingConfig_Disabled(t *testing.T) {
	t.Setenv("AI_ACCOUNTING_ENABLED", "false")
	t.Setenv("AI_ENFORCEMENT_MODE", "enforced")

	cfg := loadAIAccountingConfig()
	require.False(t, cfg.Enabled)
	require.Equal(t, domain.AIEnforcementModeEnforced, cfg.EnforcementMode)
	require.NoError(t, cfg.Validate())
}

func TestAIAccountingConfig_InvalidMode(t *testing.T) {
	t.Setenv("AI_ACCOUNTING_ENABLED", "true")
	t.Setenv("AI_ENFORCEMENT_MODE", "invalid_mode")

	cfg := loadAIAccountingConfig()
	require.Equal(t, "invalid_mode", cfg.EnforcementMode)
	require.Error(t, cfg.Validate())
	require.Contains(t, cfg.Validate().Error(), "AI_ENFORCEMENT_MODE must be either")
}

func TestAIAccountingConfig_InvalidBoolean(t *testing.T) {
	for _, value := range []string{"yes", "tru", "disable", "2", "   "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AI_ACCOUNTING_ENABLED", value)
			require.Error(t, loadAIAccountingConfig().Validate())
		})
	}
}
func TestAIAccountingConfig_NormalizedBoolean(t *testing.T) {
	t.Setenv("AI_ACCOUNTING_ENABLED", " TRUE ")
	cfg := loadAIAccountingConfig()
	require.NoError(t, cfg.Validate())
	require.True(t, cfg.Enabled)
}

func TestAIAccountingConfig_ScopedTestSubjects(t *testing.T) {
	t.Setenv("AI_ACCOUNTING_ENABLED", "true")
	t.Setenv("AI_ENFORCEMENT_MODE", "enforced")
	t.Setenv("AI_QUOTA_TEST_SUBJECTS", "2@28b76f77-27e7-4b10-ba3c-dc478dc95d35")
	cfg := loadAIAccountingConfig()
	require.NoError(t, cfg.Validate())
	require.True(t, cfg.Enabled)
	require.True(t, cfg.IsEnforced(), "global enforcement must remain on")
	require.Len(t, cfg.QuotaTestSubjects, 1)
	require.Equal(t, int64(2), cfg.QuotaTestSubjects[0].UserID)
	t.Setenv("AI_QUOTA_TEST_SUBJECTS", "2@*")
	require.ErrorContains(t, loadAIAccountingConfig().Validate(), "AI_QUOTA_TEST_SUBJECTS")
	t.Setenv("AI_QUOTA_TEST_SUBJECTS", "")
	cfg = loadAIAccountingConfig()
	require.NoError(t, cfg.Validate())
	require.Empty(t, cfg.QuotaTestSubjects)
}

package config

import (
	"fmt"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type AIAccountingConfig struct {
	Enabled             bool
	EnforcementMode     string
	QuotaTestSubjects   domain.AIQuotaTestSubjects
	invalidTestSubjects error
	invalidEnabled      bool
}

func loadAIAccountingConfig() AIAccountingConfig {
	mode := strings.ToLower(strings.TrimSpace(getEnv("AI_ENFORCEMENT_MODE", domain.AIEnforcementModeEnforced)))
	if mode == "" {
		mode = domain.AIEnforcementModeEnforced
	}
	enabled := strings.ToLower(strings.TrimSpace(getEnv("AI_ACCOUNTING_ENABLED", "true")))
	subjects, subjectsErr := domain.ParseAIQuotaTestSubjects(getEnv("AI_QUOTA_TEST_SUBJECTS", ""))
	return AIAccountingConfig{
		QuotaTestSubjects:   subjects,
		invalidTestSubjects: subjectsErr,
		Enabled:             enabled == "true" || enabled == "1",
		invalidEnabled:      enabled != "true" && enabled != "false" && enabled != "1" && enabled != "0",
		EnforcementMode:     mode,
	}
}

func (c AIAccountingConfig) Validate() error {
	if c.invalidTestSubjects != nil {
		return fmt.Errorf("AI_QUOTA_TEST_SUBJECTS: %w", c.invalidTestSubjects)
	}
	if c.invalidEnabled {
		return fmt.Errorf("AI_ACCOUNTING_ENABLED must be true, false, 1 or 0")
	}
	if c.EnforcementMode != domain.AIEnforcementModeEnforced && c.EnforcementMode != domain.AIEnforcementModeObserve {
		return fmt.Errorf("AI_ENFORCEMENT_MODE must be either %q or %q, got %q", domain.AIEnforcementModeEnforced, domain.AIEnforcementModeObserve, c.EnforcementMode)
	}
	return nil
}

func (c AIAccountingConfig) IsEnforced() bool {
	return c.EnforcementMode == domain.AIEnforcementModeEnforced
}

func (c AIAccountingConfig) IsObserve() bool {
	return c.EnforcementMode == domain.AIEnforcementModeObserve
}

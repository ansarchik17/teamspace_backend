package apperrors

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

func TestAIBudgetErrorPresentation(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		err := New(ErrCodeAIRevisionConflict, "SECRET diagnostics", 409).WithDetails(map[string]any{"scope": "company", "expected_revision": "1", "current_revision": "2", "provider_error": "SECRET"})
		body := ResponseBodyLocale(err, locale)
		require.NotContains(t, body.Error.Message, "SECRET")
		require.Equal(t, map[string]any{"scope": "company", "expected_revision": "1", "current_revision": "2"}, body.Error.Details)
		budget := ResponseBodyLocale(New(ErrCodeAIBudgetConflict, "SECRET", 409).WithDetail("reason", "BUDGET_BELOW_COMMITTED"), locale)
		require.NotEqual(t, LocalizedMessage(ErrCodeConflict, 409, locale), budget.Error.Message)
		require.Equal(t, map[string]any{"reason": "BUDGET_BELOW_COMMITTED"}, budget.Error.Details)
		for code, reason := range map[ErrorCode]string{ErrCodeAICompanySuspended: "COMPANY_SUSPENDED", ErrCodeAIConfigurationRequired: "COMPANY_BUDGET_UNCONFIGURED"} {
			body := ResponseBodyLocale(New(code, "SECRET", 409).WithDetails(map[string]any{"reason": reason, "provider_error": "SECRET"}), locale)
			require.NotEqual(t, LocalizedMessage(ErrCodeConflict, 409, locale), body.Error.Message)
			require.Equal(t, map[string]any{"reason": reason}, body.Error.Details)
			require.Empty(t, PublicDetails(code, map[string]any{"reason": "SECRET"}, locale))
		}
	}
	require.Empty(t, PublicDetails(ErrCodeAIRevisionConflict, map[string]any{"scope": "SECRET", "current_revision": "+1", "expected_revision": "9223372036854775808"}, i18n.Russian))
	require.Empty(t, PublicDetails(ErrCodeAIBudgetConflict, map[string]any{"reason": "SECRET"}, i18n.Russian))
}

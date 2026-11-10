package apperrors

import (
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"testing"
)

func TestInvitationPublicErrors(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		body := ResponseBodyLocale(New("INVITATION_REGISTRATION_LOGIN", "internal diagnostic", 409).WithDetails(map[string]any{"username": "original.login", "password": "never expose", "response": "never expose"}), locale)
		require.Equal(t, map[string]any{"username": "original.login"}, body.Error.Details)
		require.NotContains(t, body.Error.Message, "internal")
		body = ResponseBodyLocale(New(ErrCodeInvitationCodeInvalid, "internal diagnostic", 400), locale)
		require.NotContains(t, body.Error.Message, "internal")
		require.NotEmpty(t, body.Error.Message)
	}
}

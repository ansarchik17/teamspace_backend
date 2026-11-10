package apperrors

import (
	"net/http"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/i18n"
)

func TestUserIdentityConflictResponse(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		err := UserIdentityConflict()
		body := ResponseBodyLocale(err, locale)
		if err.StatusCode != http.StatusConflict || body.Error.Code != ErrCodeUserIdentityConflict {
			t.Fatalf("incorrect conflict response: %#v", body)
		}
		if !strings.Contains(body.Error.Message, "email") || !strings.Contains(body.Error.Message, "логин") {
			t.Fatalf("missing localized identity conflict message for %s: %s", locale, body.Error.Message)
		}
	}
}

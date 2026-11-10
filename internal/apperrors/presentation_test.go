package apperrors

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/i18n"
)

func TestStorageBudgetErrorsHaveSpecificLocalizedMessages(t *testing.T) {
	for _, code := range []ErrorCode{"COMPANY_BUDGET_BELOW_USAGE", "COMPANY_BUDGET_CAPACITY_EXCEEDED"} {
		for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
			result := ResponseBodyLocale(New(code, "private provider diagnostic", 409), locale).Error
			if result.Code != code || result.Message == LocalizedMessage(ErrCodeConflict, 409, locale) || strings.Contains(result.Message, "private") || result.UserMessage != result.Message {
				t.Fatalf("missing safe localized budget message for %s/%s: %+v", code, locale, result)
			}
		}
	}
}

func TestErrorPresentationIsLocalizedAndSafe(t *testing.T) {
	const diagnostic = "postgres://secret-password: SQLSTATE 23505 /private/provider"
	original := InternalWithErr(diagnostic, errors.New(diagnostic)).WithUserMessage(diagnostic).WithDetails(map[string]any{"response": diagnostic, "reason": diagnostic})
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		result := ResponseBodyLocale(original, locale)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if result.Error.Code != ErrCodeInternal || result.Error.Details != nil || result.Error.Message == diagnostic || strings.Contains(string(encoded), diagnostic) {
			t.Fatalf("unsafe payload: %s", encoded)
		}
	}
	if original.Message != diagnostic || original.UserMessage != diagnostic || !strings.Contains(original.Error(), diagnostic) {
		t.Fatal("diagnostics were modified")
	}
	if ResponseBodyLocale(original, i18n.Russian).Error.Message == ResponseBodyLocale(original, i18n.Kazakh).Error.Message {
		t.Fatal("locale did not change message")
	}
}

func TestUnknownCodeKeepsIdentityWithSafeFallback(t *testing.T) {
	for _, status := range []int{400, 403, 404, 409, 500, 502} {
		original := New("FUTURE_ERROR", "private diagnostic", status)
		for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
			result := ResponseBodyLocale(original, locale)
			if result.Error.Code != "FUTURE_ERROR" || result.Error.Message == "private diagnostic" || result.Error.Message == "" {
				t.Fatalf("unexpected result: %+v", result)
			}
		}
	}
}

func TestSafeErrorBusinessDetails(t *testing.T) {
	details := map[string]any{"quota_bytes": int64(100), "used_bytes": int64(90), "response": "secret", "reason": "SQL error"}
	result := ResponseBodyLocale(DriveQuotaExceeded(details), i18n.Kazakh)
	if len(result.Error.Details) != 2 || result.Error.Details["quota_bytes"] != int64(100) {
		t.Fatalf("lost quota details: %+v", result)
	}
	qr := ResponseBodyLocale(QRLoginRequired(), i18n.Kazakh)
	if qr.Error.Details["auth_method"] != "qr" || qr.Error.Details["title"] == "Войдите через QR-код" {
		t.Fatalf("QR details not localized: %+v", qr)
	}
	validation := ResponseBodyLocale(Validation("validator private data").WithDetails(map[string]any{"Email": "email", "Password": "required", "response": "secret"}), i18n.Kazakh)
	if len(validation.Error.Details) != 2 || validation.Error.Details["Email"] == "email" {
		t.Fatalf("validation details: %+v", validation)
	}
	malformed := ResponseBodyLocale(DriveQuotaExceeded(map[string]any{"quota_bytes": map[string]any{"response": "secret"}}), i18n.Russian)
	if malformed.Error.Details != nil {
		t.Fatal("non-numeric quota metadata was exposed")
	}
	conflict := ResponseBodyLocale(Conflict("private conflict").WithDetails(map[string]any{"conflicts": []any{map[string]any{"event_title": "User event", "response": "secret", "nested": map[string]any{"query": "secret"}}}}), i18n.Kazakh)
	encoded, err := json.Marshal(conflict)
	if err != nil || strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), "User event") {
		t.Fatalf("business metadata: %s %v", encoded, err)
	}
}

// Acceptance audit: adding an application error requires both catalog entries.
func TestDeclaredErrorCodesHaveBothTranslations(t *testing.T) {
	pattern := regexp.MustCompile(`(?:ErrorCode\s*=\s*|apperrors\.New\(\s*)"([A-Z_]+)"`)
	found := make(map[string]bool)
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			found[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 70 {
		t.Fatalf("audit did not find existing codes: %d", len(found))
	}
	for code := range found {
		for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
			if _, err := i18n.Render(locale, "error."+code, nil); err != nil {
				t.Errorf("%s/%s: %v", locale, code, err)
			}
		}
	}
}

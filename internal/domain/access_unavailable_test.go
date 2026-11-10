package domain

import (
	"errors"
	"testing"
)

func TestUnavailablePermissionsAreVisibleButCannotBeDelegated(t *testing.T) {
	for app, codes := range AccessUnavailablePermissions() {
		registered := map[string]bool{}
		for _, item := range AccessCatalog()[app] {
			registered[item.Code] = true
		}
		for _, code := range codes {
			if !registered[code] {
				t.Fatalf("unknown unavailable permission: %s %s", app, code)
			}
			if _, err := ValidateAccessPermissions(app, []string{code}); !errors.Is(err, ErrAccessPermissionUnavailable) {
				t.Fatalf("inert grant accepted: %s %s: %v", app, code, err)
			}
		}
	}
	for _, code := range []string{"settings:create", "settings:update", "settings:delete", "reports:export"} {
		if _, err := ValidateAccessPermissions("sd", []string{code}); err != nil {
			t.Fatalf("implemented operation disabled: %s %v", code, err)
		}
	}
}

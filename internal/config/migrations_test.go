package config

import "testing"

func TestDatabaseMigrationsEnabled(t *testing.T) {
	for _, value := range []string{"", " ", "1", "true", "TRUE", "yes", " YES ", "on", " On ", "unexpected"} {
		if !DatabaseMigrationsEnabled(value) {
			t.Errorf("migrations unexpectedly disabled for %q", value)
		}
	}
	for _, value := range []string{"0", "false", "FALSE", " False ", "no", " NO ", "off", " Off "} {
		if DatabaseMigrationsEnabled(value) {
			t.Errorf("migrations unexpectedly enabled for %q", value)
		}
	}
}

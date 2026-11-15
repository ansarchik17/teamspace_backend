package config

import (
	"log"
	"strings"
)

// DatabaseMigrationsEnabled keeps API startup and the standalone runner on the
// same switch. An unset value preserves automatic migrations for local dev.
func DatabaseMigrationsEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return false
	case "", "1", "true", "yes", "on":
		return true
	default:
		log.Println("unrecognized RUN_DB_MIGRATIONS value; migrations remain enabled")
		return true
	}
}

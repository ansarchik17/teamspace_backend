package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseCalendarRRULE accepts the RFC 5545 subset needed by CAL-003. The
// database keeps the normalized frequency, interval and inclusive UNTIL in
// separate columns so existing events remain compatible.
func parseCalendarRRULE(input *string) (*string, *int, *time.Time, error) {
	if input == nil {
		return nil, nil, nil, nil
	}
	value := strings.ToUpper(strings.TrimSpace(*input))
	if value == "" || value == "NONE" || value == "NULL" {
		return nil, nil, nil, nil
	}
	if strings.HasPrefix(value, "RRULE:") {
		value = strings.TrimPrefix(value, "RRULE:")
	}
	if !strings.Contains(value, "=") {
		if err := validateRecurrence(&value); err != nil {
			return nil, nil, nil, err
		}
		return &value, nil, nil, nil
	}
	parts := strings.Split(value, ";")
	seen := make(map[string]bool)
	var freq string
	var interval *int
	var until *time.Time
	for _, part := range parts {
		key, raw, ok := strings.Cut(part, "=")
		if !ok || raw == "" || seen[key] {
			return nil, nil, nil, fmt.Errorf("invalid RRULE component")
		}
		seen[key] = true
		switch key {
		case "FREQ":
			freq = raw
		case "INTERVAL":
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 3650 {
				return nil, nil, nil, fmt.Errorf("invalid RRULE INTERVAL")
			}
			interval = &n
		case "UNTIL":
			t, err := time.Parse("20060102T150405Z", raw)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("invalid RRULE UNTIL; use UTC YYYYMMDDTHHMMSSZ")
			}
			until = &t
		default:
			return nil, nil, nil, fmt.Errorf("unsupported RRULE component: %s", key)
		}
	}
	if err := validateRecurrence(&freq); err != nil {
		return nil, nil, nil, err
	}
	return &freq, interval, until, nil
}

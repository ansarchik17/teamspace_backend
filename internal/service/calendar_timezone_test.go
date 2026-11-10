package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestGetSupportedTimezones(t *testing.T) {
	t.Parallel()

	timezones := GetSupportedTimezones()
	require.NotEmpty(t, timezones)

	var foundDefault bool
	var foundAlmaty bool
	var foundMoscow bool
	var foundUTC bool

	for _, tz := range timezones {
		if tz.IsDefault {
			foundDefault = true
			assert.Equal(t, "Asia/Almaty", tz.ID)
		}
		if tz.ID == "Asia/Almaty" {
			foundAlmaty = true
			assert.Equal(t, "+05:00", tz.Offset)
			assert.Equal(t, 300, tz.OffsetMinutes)
		}
		if tz.ID == "Europe/Moscow" {
			foundMoscow = true
			assert.Equal(t, "+03:00", tz.Offset)
			assert.Equal(t, 180, tz.OffsetMinutes)
		}
		if tz.ID == "UTC" {
			foundUTC = true
			assert.Equal(t, "+00:00", tz.Offset)
			assert.Equal(t, 0, tz.OffsetMinutes)
		}
	}

	assert.True(t, foundDefault, "Asia/Almaty must be default timezone")
	assert.True(t, foundAlmaty, "Asia/Almaty must be in list")
	assert.True(t, foundMoscow, "Europe/Moscow must be in list")
	assert.True(t, foundUTC, "UTC must be in list")
}

func TestConvertTimezoneItem(t *testing.T) {
	t.Parallel()

	// 10:00 UTC
	baseUTC := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	endUTC := baseUTC.Add(time.Hour)

	// Convert to Asia/Almaty (UTC+5) -> 15:00
	item, err := ConvertTimezoneItem(baseUTC, endUTC, "Asia/Almaty")
	require.NoError(t, err)
	assert.Equal(t, "Asia/Almaty", item.Timezone)
	assert.Equal(t, "+05:00", item.Offset)
	assert.Equal(t, 300, item.OffsetMinutes)
	assert.Equal(t, "2026-09-28 15:00", item.StartTimeFormatted)
	assert.Equal(t, "2026-09-28 16:00", item.EndTimeFormatted)
	assert.Equal(t, 15, item.StartTime.Hour())

	// Convert to Europe/Moscow (UTC+3) -> 13:00
	itemMsk, err := ConvertTimezoneItem(baseUTC, endUTC, "Europe/Moscow")
	require.NoError(t, err)
	assert.Equal(t, "Europe/Moscow", itemMsk.Timezone)
	assert.Equal(t, "+03:00", itemMsk.Offset)
	assert.Equal(t, 180, itemMsk.OffsetMinutes)
	assert.Equal(t, "2026-09-28 13:00", itemMsk.StartTimeFormatted)
	assert.Equal(t, 13, itemMsk.StartTime.Hour())

	// Invalid timezone
	_, err = ConvertTimezoneItem(baseUTC, endUTC, "Invalid/Nonexistent")
	assert.Error(t, err)
}

func TestFormatEventLocalTimes(t *testing.T) {
	t.Parallel()

	startUTC := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	endUTC := startUTC.Add(time.Hour)

	event := &domain.Event{
		ID:        uuid.New(),
		StartTime: startUTC,
		EndTime:   endUTC,
		Timezone:  "Asia/Almaty",
	}

	FormatEventLocalTimes(event, "Asia/Almaty")
	require.NotNil(t, event.StartTimeLocal)
	require.NotNil(t, event.EndTimeLocal)
	assert.Contains(t, *event.StartTimeLocal, "+05:00")
	assert.Contains(t, *event.StartTimeLocal, "15:00:00")

	// Target different timezone
	FormatEventLocalTimes(event, "Europe/Moscow")
	assert.Contains(t, *event.StartTimeLocal, "+03:00")
	assert.Contains(t, *event.StartTimeLocal, "13:00:00")
}

func TestFormatCountdownRU(t *testing.T) {
	t.Parallel()

	tests := []struct {
		minutes  int
		expected string
	}{
		{0, "прямо сейчас"},
		{-5, "прямо сейчас"},
		{1, "через 1 минуту"},
		{2, "через 2 минуты"},
		{4, "через 4 минуты"},
		{5, "через 5 минут"},
		{10, "через 10 минут"},
		{15, "через 15 минут"},
		{21, "через 21 минуту"},
		{22, "через 22 минуты"},
		{25, "через 25 минут"},
		{60, "через 1 час"},
		{120, "через 2 часа"},
		{300, "через 5 часов"},
		{90, "через 1 час 30 минут"},
		{1440, "через 1 день (24 часа)"},
	}

	for _, tt := range tests {
		actual := FormatCountdownRU(tt.minutes)
		assert.Equal(t, tt.expected, actual, "FormatCountdownRU(%d)", tt.minutes)
	}
}

func TestGetReminderPresets(t *testing.T) {
	t.Parallel()

	presets := GetReminderPresets()
	require.NotEmpty(t, presets)

	presetMinutes := make(map[int]bool)
	for _, p := range presets {
		presetMinutes[p.MinutesBefore] = true
		assert.NotEmpty(t, p.Label)
		assert.NotEmpty(t, p.Description)
	}

	assert.True(t, presetMinutes[0], "0 min preset required")
	assert.True(t, presetMinutes[5], "5 min preset required")
	assert.True(t, presetMinutes[15], "15 min preset required")
	assert.True(t, presetMinutes[60], "60 min preset required")
	assert.True(t, presetMinutes[1440], "1440 min preset required")
}

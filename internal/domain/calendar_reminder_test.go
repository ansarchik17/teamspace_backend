package domain

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCalendarReminderPresence(t *testing.T) {
	for _, tc := range []struct {
		body    string
		set     bool
		minutes *int
	}{
		{`{}`, false, nil},
		{`{"reminder_minutes":null}`, true, nil},
		{`{"reminder_minutes":0}`, true, intPointerForReminder(0)},
		{`{"reminder_minutes":30}`, true, intPointerForReminder(30)},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var create CreateEventRequest
			require.NoError(t, json.Unmarshal([]byte(tc.body), &create))
			require.Equal(t, tc.set, create.ReminderMinutesSet)
			require.Equal(t, tc.minutes, create.ReminderMinutes)
			var update UpdateEventRequest
			require.NoError(t, json.Unmarshal([]byte(tc.body), &update))
			require.Equal(t, tc.set, update.ReminderMinutesSet)
			require.Equal(t, tc.minutes, update.ReminderMinutes)
		})
	}
}

func intPointerForReminder(value int) *int { return &value }

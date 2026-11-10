package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
	"time"
)

func TestCalendarPreferencesRoundTripIntegration(t *testing.T) {
	svc, pool := roomIntegrationService(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	end := start.Add(time.Hour)
	create := func(body string) *domain.Event {
		t.Helper()
		var req domain.CreateEventRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		req.Title, req.StartTime, req.EndTime = "Preferences", start, end
		event, err := svc.CreateEvent(ctx, 1, &req)
		require.NoError(t, err)
		return event
	}
	event := create(`{"reminder_minutes":null}`)
	require.Nil(t, event.ReminderMinutes)
	require.NotZero(t, event.CalendarID)
	saved, err := svc.GetEvent(ctx, 1, event.ID)
	require.NoError(t, err)
	require.Equal(t, event.CalendarID, saved.CalendarID)
	require.Nil(t, saved.ReminderMinutes)

	update := func(body string) *domain.Event {
		t.Helper()
		var req domain.UpdateEventRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		req.CalendarID = &event.CalendarID
		saved, err := svc.UpdateEvent(ctx, 1, event.ID, &req)
		require.NoError(t, err)
		return saved
	}
	saved = update(`{"title":"Renamed","reminder_minutes":30,"attendee_ids":[2]}`)
	require.Equal(t, 30, *saved.ReminderMinutes)
	saved = update(`{"reminder_minutes":null,"attendee_ids":[2]}`)
	require.Nil(t, saved.ReminderMinutes)
	// An unrelated edit must not re-enable either participant's disabled reminder.
	require.NoError(t, svc.DeleteReminder(ctx, 2, event.ID))
	saved = update(`{"title":"Renamed again","attendee_ids":[2]}`)
	require.Nil(t, saved.ReminderMinutes)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM event_reminders WHERE event_id=$1`, event.ID).Scan(&count))
	require.Zero(t, count)
	saved, err = svc.GetEvent(ctx, 1, event.ID)
	require.NoError(t, err)
	require.Equal(t, event.CalendarID, saved.CalendarID)
	require.Nil(t, saved.ReminderMinutes)
	saved = update(`{"reminder_minutes":0}`)
	require.NotNil(t, saved.ReminderMinutes)
	require.Zero(t, *saved.ReminderMinutes)
	saved = update(`{"title":"Keep reminder"}`)
	require.NotNil(t, saved.ReminderMinutes)
	require.Zero(t, *saved.ReminderMinutes)

	legacy := create(`{}`)
	require.NotNil(t, legacy.ReminderMinutes)
	require.Equal(t, 15, *legacy.ReminderMinutes)
}

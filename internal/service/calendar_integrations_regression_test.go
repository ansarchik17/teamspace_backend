package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"testing"
	"time"
)

func TestCalendarIntegrationTimezoneValidation(t *testing.T) {
	for _, tz := range []string{"", "Local", "invalid/zone"} {
		require.Error(t, ValidateCalendarTimezone(tz))
	}
	require.NoError(t, ValidateCalendarTimezone("America/New_York"))
	s := &CalendarService{}
	start := time.Now().UTC()
	end := start.Add(time.Hour)
	_, err := s.ConvertTimezones(context.Background(), 1, domain.TimezoneConvertRequest{StartTime: &start, EndTime: &start, TargetTimezones: []string{"UTC"}})
	require.Error(t, err)
	_, err = s.ConvertTimezones(context.Background(), 1, domain.TimezoneConvertRequest{StartTime: &start, EndTime: &end, TargetTimezones: make([]string, 51)})
	require.Error(t, err)
}

func TestCalendarIntegrationQMeetRoomStableAndUnique(t *testing.T) {
	p := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{}, nil, zap.NewNop())
	first := uuid.MustParse("12345678-1234-1234-1234-123456789abc")
	second := uuid.MustParse("12345678-1234-1234-1234-123456789abd")
	a := p.GenerateQMeetRoomInfo(first, "Original")
	b := p.GenerateQMeetRoomInfo(first, "Changed title")
	c := p.GenerateQMeetRoomInfo(second, "Original")
	require.Equal(t, *a.MeetingRoomID, *b.MeetingRoomID)
	require.NotEqual(t, *a.MeetingRoomID, *c.MeetingRoomID)
	require.Contains(t, *a.MeetingURL, "/web/calls/calendar/")
}

func TestCalendarIntegrationAccessAndReminderFields(t *testing.T) {
	s, pool := roomIntegrationService(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	minutes := 15
	event, err := s.CreateEvent(ctx, 1, &domain.CreateEventRequest{Title: "CAL007", StartTime: start, EndTime: start.Add(time.Hour), Timezone: "Europe/Berlin", ReminderMinutes: &minutes})
	require.NoError(t, err)
	_, err = s.ConvertTimezones(ctx, 2, domain.TimezoneConvertRequest{EventID: &event.ID, TargetTimezones: []string{"UTC"}})
	require.Error(t, err)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	_, err = pool.Exec(ctx, "INSERT INTO event_attendees(event_id,user_id,status,created_at) VALUES($1,2,'accepted',now())", event.ID)
	require.NoError(t, err)
	_, err = s.GenerateQMeetLink(ctx, 2, event.ID)
	require.Error(t, err)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	link, err := s.GenerateQMeetLink(ctx, 1, event.ID)
	require.NoError(t, err)
	rows, err := s.repo.ListRemindersWithEvents(ctx, start.Add(-time.Hour), start.Add(2*time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Europe/Berlin", rows[0].Timezone)
	require.True(t, rows[0].IsOnline)
	require.Equal(t, "qmeet", rows[0].MeetingProvider)
	require.Equal(t, link.MeetingURL, *rows[0].MeetingURL)
	s.meetingProviderService = NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{}, nil, zap.NewNop())
	provider := "qmeet"
	online := true
	updated, err := s.UpdateEvent(ctx, 1, event.ID, &domain.UpdateEventRequest{MeetingProvider: &provider, IsOnline: &online})
	require.NoError(t, err)
	require.Equal(t, "qmeet", updated.MeetingProvider)
	loaded, err := s.repo.GetByID(ctx, event.ID)
	require.NoError(t, err)
	require.Equal(t, "qmeet", loaded.MeetingProvider)
	_, err = s.ConvertTimezones(ctx, 2, domain.TimezoneConvertRequest{EventID: &event.ID, TargetTimezones: []string{"UTC"}})
	require.NoError(t, err)
}

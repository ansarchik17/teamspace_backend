package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestResolveCalendarGridRange(t *testing.T) {
	start, end, location, err := resolveCalendarGridRange(domain.CalendarGridRequest{
		View: domain.CalendarViewWeek, Date: "2026-09-27", Timezone: "Asia/Almaty",
	})
	require.NoError(t, err)
	require.Equal(t, "Asia/Almaty", location.String())
	require.Equal(t, "2026-09-21", start.Format("2006-01-02"))
	require.Equal(t, "2026-09-28", end.Format("2006-01-02"))
}

func TestResolveCalendarGridRangeRejectsInvalidInput(t *testing.T) {
	_, _, _, err := resolveCalendarGridRange(domain.CalendarGridRequest{View: domain.CalendarViewDay, Date: "2026-09-24", Timezone: "Asia/Almatyy"})
	require.Equal(t, apperrors.ErrCodeBadRequest, apperrors.GetAppError(err).Code)

	_, _, _, err = resolveCalendarGridRange(domain.CalendarGridRequest{View: "agenda", Date: "2026-09-24"})
	require.Equal(t, apperrors.ErrCodeBadRequest, apperrors.GetAppError(err).Code)
}

func TestValidateCalendarVisibilityRequest(t *testing.T) {
	require.Error(t, validateCalendarVisibilityRequest(domain.UpdateCalendarVisibilityRequest{}))
	negative := -1
	require.Error(t, validateCalendarVisibilityRequest(domain.UpdateCalendarVisibilityRequest{DisplayOrder: &negative}))
	valid := 42
	require.NoError(t, validateCalendarVisibilityRequest(domain.UpdateCalendarVisibilityRequest{DisplayOrder: &valid}))
}

func TestExpandCalendarGridEventsIncludesRecurringOccurrences(t *testing.T) {
	rule := "WEEKLY"
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	event := domain.Event{
		ID: uuid.New(), StartTime: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC),
		EndTime: time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC), RecurrenceRule: &rule, RecurrenceInterval: 1,
	}

	result := expandCalendarGridEvents([]domain.Event{event}, from, to)
	require.Len(t, result, 1)
	require.Equal(t, time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC), result[0].StartTime)
	require.NotEmpty(t, result[0].OccurrenceID)
}

func TestExpandRecurringLongLivedSeriesStillAppears(t *testing.T) {
	rule := "DAILY"
	start := time.Date(2010, 1, 1, 10, 0, 0, 0, time.UTC)
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	event := domain.Event{ID: uuid.New(), StartTime: start, EndTime: start.Add(time.Hour), RecurrenceRule: &rule, RecurrenceInterval: 1}
	got := expandRecurring(event, from, from.AddDate(0, 0, 1))
	require.Len(t, got, 1)
	require.Equal(t, time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC), got[0].StartTime)
	require.NotEmpty(t, got[0].OccurrenceID)
}

func TestExpandGroupCalendarGridEventsPreservesGroup(t *testing.T) {
	rule := "DAILY"
	groupID := uuid.New()
	from := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	event := domain.GroupEventInfo{
		Event:   domain.Event{StartTime: from.AddDate(0, 0, -2).Add(10 * time.Hour), EndTime: from.AddDate(0, 0, -2).Add(11 * time.Hour), RecurrenceRule: &rule, RecurrenceInterval: 1},
		GroupID: groupID, GroupName: "Team",
	}

	result := expandGroupCalendarGridEvents([]domain.GroupEventInfo{event}, from, to)
	require.Len(t, result, 1)
	require.Equal(t, groupID, result[0].GroupID)
	require.Equal(t, "Team", result[0].GroupName)
}

package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestCalendarRRULESubset(t *testing.T) {
	input := "RRULE:FREQ=WEEKLY;INTERVAL=2;UNTIL=20261031T100000Z"
	rule, interval, until, err := parseCalendarRRULE(&input)
	require.NoError(t, err)
	require.Equal(t, "WEEKLY", *rule)
	require.Equal(t, 2, *interval)
	require.Equal(t, time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC), *until)
	for _, invalid := range []string{"RRULE:FREQ=DAILY;BYDAY=MO", "RRULE:FREQ=DAILY;INTERVAL=0", "RRULE:FREQ=YEARLY", "RRULE:FREQ=DAILY;FREQ=WEEKLY"} {
		_, _, _, err := parseCalendarRRULE(&invalid)
		require.Error(t, err, invalid)
	}
}

func TestCalendarExceptionsOnlyChangeSelectedOccurrence(t *testing.T) {
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	rule := "DAILY"
	event := domain.Event{ID: uuid.New(), Title: "Daily", StartTime: start, EndTime: start.Add(time.Hour), RecurrenceRule: &rule, RecurrenceInterval: 1}
	modifiedStart := start.AddDate(0, 0, 1)
	movedStart := modifiedStart.Add(2 * time.Hour)
	deletedStart := start.AddDate(0, 0, 2)
	items := []domain.CalendarEventException{
		{EventID: event.ID, OccurrenceDate: modifiedStart, ExceptionType: "modified", EventData: &domain.OccurrenceDetails{Title: "Moved", StartTime: movedStart, EndTime: movedStart.Add(time.Hour)}},
		{EventID: event.ID, OccurrenceDate: deletedStart, ExceptionType: "deleted"},
	}
	got := applyCalendarExceptions([]domain.Event{event}, items, start, start.AddDate(0, 0, 4))
	require.Len(t, got, 3)
	require.Equal(t, "Daily", got[0].Title)
	require.Equal(t, "Moved", got[1].Title)
	require.Equal(t, movedStart, got[1].StartTime)
	require.Equal(t, event.ID.String()+":"+fmt.Sprint(modifiedStart.Unix()), got[1].OccurrenceID)
	require.Equal(t, start.AddDate(0, 0, 3), got[2].StartTime)
}

func TestCalendarMovedOccurrenceAppearsOutsideOriginalRange(t *testing.T) {
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	rule := "WEEKLY"
	event := domain.Event{ID: uuid.New(), Title: "Weekly", StartTime: start, EndTime: start.Add(time.Hour), RecurrenceRule: &rule, RecurrenceInterval: 1}
	moved := start.AddDate(0, 0, 2)
	items := []domain.CalendarEventException{{EventID: event.ID, OccurrenceDate: start, ExceptionType: "modified", EventData: &domain.OccurrenceDetails{Title: "Moved", StartTime: moved, EndTime: moved.Add(time.Hour)}}}
	got := applyCalendarExceptions([]domain.Event{event}, items, moved.Add(-time.Hour), moved.Add(2*time.Hour))
	require.Len(t, got, 1)
	require.Equal(t, moved, got[0].StartTime)
}

func TestOriginalOccurrenceAcceptsUnixSecondIDForFractionalStart(t *testing.T) {
	start := time.Date(2026, 9, 25, 10, 0, 0, 250000000, time.UTC)
	rule := "DAILY"
	event := domain.Event{ID: uuid.New(), StartTime: start, EndTime: start.Add(time.Hour), RecurrenceRule: &rule, RecurrenceInterval: 1}
	got, err := originalOccurrence(event, time.Unix(start.AddDate(0, 0, 1).Unix(), 0))
	require.NoError(t, err)
	require.Equal(t, start.AddDate(0, 0, 1), got.StartTime)
}

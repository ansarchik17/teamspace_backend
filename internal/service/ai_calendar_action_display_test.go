package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type calendarCancellationStub struct {
	event              *domain.Event
	readErr, deleteErr error
	readID, deletedID  uuid.UUID
	userID             int64
}

func (s *calendarCancellationStub) GetEvent(_ context.Context, userID int64, id uuid.UUID) (*domain.Event, error) {
	s.readID = id
	s.userID = userID
	return s.event, s.readErr
}
func (s *calendarCancellationStub) DeleteEvent(_ context.Context, userID int64, id uuid.UUID) error {
	s.deletedID = id
	return s.deleteErr
}

func TestMainChatCalendarCancellationUsesLiveEventTitle(t *testing.T) {
	id := uuid.New()
	calendar := &calendarCancellationStub{event: &domain.Event{ID: id, Title: "Встреча с t.company@a.teamspace.kz"}}
	event, err := cancelCalendarActionEvent(context.Background(), calendar, 42, id)
	require.NoError(t, err)
	require.Equal(t, "Встреча с t.company@a.teamspace.kz", event.Title)
	require.Equal(t, id, calendar.readID)
	require.Equal(t, id, calendar.deletedID)
	require.EqualValues(t, 42, calendar.userID)
}

func TestMainChatCalendarCancellationReadFailureDoesNotDelete(t *testing.T) {
	id := uuid.New()
	for _, calendar := range []*calendarCancellationStub{
		{readErr: errors.New("forbidden")},
		{},
		{event: &domain.Event{ID: uuid.New(), Title: "Another event"}},
	} {
		event, err := cancelCalendarActionEvent(context.Background(), calendar, 42, id)
		require.Error(t, err)
		require.Nil(t, event)
		require.Equal(t, uuid.Nil, calendar.deletedID)
	}
	calendar := &calendarCancellationStub{event: &domain.Event{ID: id, Title: "Real title"}, deleteErr: errors.New("delete denied")}
	event, err := cancelCalendarActionEvent(context.Background(), calendar, 42, id)
	require.Error(t, err)
	require.Nil(t, event, "failed mutation must not return a success event")
}

func TestMainChatCalendarPreviewDistinguishesSelectorFromTitle(t *testing.T) {
	start, err := time.Parse(time.RFC3339, "2026-10-24T10:00:00+05:00")
	require.NoError(t, err)
	event := &domain.Event{ID: uuid.New(), Title: "Встреча с t.company@a.teamspace.kz", StartTime: start, EndTime: start.Add(time.Hour)}
	fields := chatCalendarActionFields(map[string]any{"event_id": event.ID.String(), "event_query": "завтра"}, event)
	values := map[string]string{}
	for _, field := range fields {
		values[field.Label] = field.Value
	}
	require.Equal(t, "завтра", values["Выбор встречи"])
	require.Equal(t, event.Title, values["Название"])
	require.Equal(t, "2026-10-24T10:00:00+05:00", values["Начало"])
	require.Equal(t, "2026-10-24T11:00:00+05:00", values["Окончание"])
}

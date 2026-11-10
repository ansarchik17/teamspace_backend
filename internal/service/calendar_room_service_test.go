package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type roomRoleUsers struct{ user *domain.User }

func (r roomRoleUsers) GetByID(context.Context, int64) (*domain.User, error) { return r.user, nil }

func TestRoomManagementRequiresActiveAdministrator(t *testing.T) {
	for _, tc := range []struct {
		role    domain.Role
		active  bool
		allowed bool
	}{
		{domain.RoleUser, true, false}, {domain.RoleManager, true, false}, {domain.RoleSuperAdmin, false, false}, {domain.RoleSuperAdmin, true, true},
	} {
		err := requireRoomAdmin(context.Background(), roomRoleUsers{&domain.User{Role: tc.role, IsActive: tc.active}}, 1)
		if tc.allowed {
			require.NoError(t, err)
		} else {
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		}
	}
	s := &CalendarService{}
	_, err := s.CreateRoom(context.Background(), 1, domain.CreateMeetingRoomRequest{Name: "Room", Capacity: 4})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
}

func TestRoomAvailabilityQueryLimits(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		duration time.Duration
		step     int
	}{
		{-time.Hour, 30}, {91 * 24 * time.Hour, 30}, {90 * 24 * time.Hour, 5}, {time.Hour, -1}, {time.Hour, 241},
	} {
		_, err := normalizeRoomAvailabilityQuery(domain.RoomAvailabilityQuery{StartTime: start, EndTime: start.Add(tc.duration), SlotStepMinutes: tc.step})
		require.Error(t, err)
		require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	}
	q, err := normalizeRoomAvailabilityQuery(domain.RoomAvailabilityQuery{StartTime: start, EndTime: start.Add(time.Hour)})
	require.NoError(t, err)
	require.Equal(t, 30, q.SlotStepMinutes)
}

func TestBuildRoomSeriesReservesEveryOccurrenceAndAppliesExceptions(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 60)
	rule := "DAILY"
	room, other := uuid.New(), uuid.New()
	event := domain.Event{ID: uuid.New(), UserID: 1, Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), RoomID: &room, RecurrenceRule: &rule, RecurrenceUntil: &until, RecurrenceInterval: 1}
	bookings, err := buildEventRoomBookings(event, nil)
	require.NoError(t, err)
	require.Len(t, bookings, 61)
	exceptions := []domain.CalendarEventException{
		{EventID: event.ID, OccurrenceDate: start, ExceptionType: "deleted"},
		{EventID: event.ID, OccurrenceDate: start.AddDate(0, 0, 1), ExceptionType: "modified", EventData: &domain.OccurrenceDetails{Title: "Moved", StartTime: until.AddDate(0, 0, 2), EndTime: until.AddDate(0, 0, 2).Add(time.Hour), RoomID: &other}},
	}
	bookings, err = buildEventRoomBookings(event, exceptions)
	require.NoError(t, err)
	require.Len(t, bookings, 60)
	for _, b := range bookings {
		require.False(t, b.StartTime.Equal(start))
		require.False(t, b.StartTime.Equal(start.AddDate(0, 0, 1)))
	}
	require.Equal(t, other, bookings[len(bookings)-1].RoomID)
	require.Equal(t, "Moved", bookings[len(bookings)-1].Title)
	event.RecurrenceUntil = nil
	_, err = buildEventRoomBookings(event, nil)
	require.Error(t, err)
	tooLate := start.AddDate(2, 0, 0)
	event.RecurrenceUntil = &tooLate
	_, err = buildEventRoomBookings(event, nil)
	require.Error(t, err)
}

func TestRoomOccurrenceCanReleaseRoomAndLegacyOverridesInheritRoom(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	rule := "DAILY"
	room := uuid.New()
	event := domain.Event{ID: uuid.New(), UserID: 1, Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), RoomID: &room, RecurrenceRule: &rule, RecurrenceUntil: &start}
	details := &domain.OccurrenceDetails{Title: "Legacy override", StartTime: start, EndTime: start.Add(time.Hour)}
	ex := domain.CalendarEventException{EventID: event.ID, OccurrenceDate: start, ExceptionType: "modified", EventData: details}
	bookings, err := buildEventRoomBookings(event, []domain.CalendarEventException{ex})
	require.NoError(t, err)
	require.Len(t, bookings, 1)
	require.Equal(t, room, bookings[0].RoomID)
	none := uuid.Nil
	details.RoomID = &none
	bookings, err = buildEventRoomBookings(event, []domain.CalendarEventException{ex})
	require.NoError(t, err)
	require.Empty(t, bookings)
}

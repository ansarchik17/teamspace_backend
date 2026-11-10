package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestCalendarEventNotFoundError(t *testing.T) {
	t.Parallel()

	err := calendarEventNotFoundError()
	require.NotNil(t, err)
	assert.Equal(t, apperrors.ErrCodeNotFound, err.Code)
	assert.Equal(t, http.StatusNotFound, err.StatusCode)
	assert.Equal(t, "Событие не найдено", err.Message)
}

func TestCalendarEventAccessDeniedError(t *testing.T) {
	t.Parallel()

	err := calendarEventAccessDeniedError()
	require.NotNil(t, err)
	assert.Equal(t, apperrors.ErrCodeForbidden, err.Code)
	assert.Equal(t, http.StatusForbidden, err.StatusCode)
	assert.Equal(t, "У вас нет доступа к этому событию", err.Message)
}

func TestUserCanAccessEvent(t *testing.T) {
	t.Parallel()

	event := &domain.Event{
		UserID: 10,
		Attendees: []domain.EventAttendee{
			{UserID: 20},
			{UserID: 30},
		},
	}

	assert.True(t, userCanAccessEvent(10, event))
	assert.True(t, userCanAccessEvent(20, event))
	assert.False(t, userCanAccessEvent(99, event))
	assert.False(t, userCanAccessEvent(99, nil))
}

func TestEventVisibleInUserCalendar(t *testing.T) {
	t.Parallel()

	event := &domain.Event{
		UserID: 15,
		Attendees: []domain.EventAttendee{
			{UserID: 25},
			{UserID: 35},
		},
	}

	assert.True(t, eventVisibleInUserCalendar(15, event))
	assert.True(t, eventVisibleInUserCalendar(25, event))
	assert.False(t, eventVisibleInUserCalendar(99, event))
	assert.False(t, eventVisibleInUserCalendar(99, nil))
}

func TestSetEventOwnerIdentityFromUser(t *testing.T) {
	t.Parallel()

	event := &domain.Event{UserID: 9}
	user := &domain.User{
		ID:        9,
		Username:  "owner9",
		FirstName: "Иван",
		LastName:  "Петров",
	}

	setEventOwnerIdentityFromUser(event, user)

	require.NotNil(t, event.Username)
	require.NotNil(t, event.FirstName)
	require.NotNil(t, event.LastName)
	assert.Equal(t, "owner9", *event.Username)
	assert.Equal(t, "Иван", *event.FirstName)
	assert.Equal(t, "Петров", *event.LastName)
}

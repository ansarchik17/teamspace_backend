package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestRedactCalendarAttendeesForInvitee(t *testing.T) {
	note := "private response"
	guests := []domain.EventAttendee{
		{UserID: 1, Comment: &note, User: &domain.UserResponse{ID: 1, Username: "owner", Email: "owner@example.test", FirstName: "Owner", IIN: &note}},
		{UserID: 2, Comment: &note, User: &domain.UserResponse{ID: 2, Username: "viewer", Email: "viewer@example.test"}},
		{UserID: 3, Comment: &note, User: &domain.UserResponse{ID: 3, Username: "guest", Email: "guest@example.test"}},
	}
	redactCalendarAttendees(guests, 2, 1)
	require.Nil(t, guests[0].Comment)
	require.Empty(t, guests[0].User.Email)
	require.Nil(t, guests[0].User.IIN)
	require.Equal(t, "owner", guests[0].User.Username)
	require.Equal(t, "viewer@example.test", guests[1].User.Email)
	require.NotNil(t, guests[1].Comment)
	require.Empty(t, guests[2].User.Email)
}

func TestRedactCalendarAttendeesKeepsOrganizerView(t *testing.T) {
	note := "response"
	guests := []domain.EventAttendee{{UserID: 2, Comment: &note, User: &domain.UserResponse{ID: 2, Email: "guest@example.test"}}}
	redactCalendarAttendees(guests, 1, 1)
	require.Equal(t, "guest@example.test", guests[0].User.Email)
	require.Equal(t, note, *guests[0].Comment)
}

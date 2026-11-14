package service

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestContactQuickActions(t *testing.T) {
	work := " work+tag@example.com "
	profile := &domain.ContactProfile{Contact: domain.Contact{ID: 2, FullName: "Анна Иванова", Email: "login@example.com"}, WorkEmail: &work}
	actions := buildContactActions(1, profile)
	require.Equal(t, "work+tag@example.com", actions.Email.Address)
	require.True(t, actions.Email.Enabled)
	parsed, err := url.Parse(actions.Email.MailtoURL)
	require.NoError(t, err)
	require.Equal(t, "mailto", parsed.Scheme)
	require.Empty(t, parsed.RawQuery)
	require.Empty(t, parsed.Fragment)
	recipient, err := url.PathUnescape(parsed.Opaque)
	require.NoError(t, err)
	require.Equal(t, actions.Email.Address, recipient)
	require.True(t, actions.Meeting.Enabled)
	require.Equal(t, []int64{2}, actions.Meeting.AttendeeIDs)
	require.Equal(t, "Встреча: Анна Иванова", actions.Meeting.Title)
	require.Equal(t, "/api/v1/calendar/events", actions.Meeting.Endpoint)
	require.Equal(t, "/api/v1/calendar/events/check-availability", actions.Meeting.AvailabilityEndpoint)
	own := buildContactActions(2, profile)
	require.False(t, own.Meeting.Enabled)
	require.Empty(t, own.Meeting.AttendeeIDs)
	require.True(t, own.Email.Enabled)
}

func TestContactEmailShortcutEscapesHeadersAndFallsBack(t *testing.T) {
	for _, work := range []string{"", "bad", "Name <mail@example.com>", "mail@example.com\r\nBcc:other@example.com"} {
		actions := buildContactActions(1, &domain.ContactProfile{Contact: domain.Contact{ID: 2, Email: "fallback@example.com"}, WorkEmail: &work})
		require.True(t, actions.Email.Enabled)
		require.Equal(t, "fallback@example.com", actions.Email.Address)
	}
	actions := buildContactActions(1, &domain.ContactProfile{Contact: domain.Contact{ID: 2, Email: "bad"}})
	require.False(t, actions.Email.Enabled)
	require.Equal(t, "email_unavailable", actions.Email.DisabledReason)
	require.Empty(t, actions.Email.MailtoURL)
	// Valid local parts may contain URI delimiters, which must stay in the recipient.
	for _, address := range []string{"name?subject=unsafe@example.com", "name#fragment@example.com", "name%tag@example.com"} {
		require.True(t, domain.ValidContactEmail(address))
		actions = buildContactActions(1, &domain.ContactProfile{Contact: domain.Contact{ID: 2, Email: address}})
		parsed, err := url.Parse(actions.Email.MailtoURL)
		require.NoError(t, err)
		require.Empty(t, parsed.RawQuery)
		require.Empty(t, parsed.Fragment)
		decoded, err := url.PathUnescape(parsed.Opaque)
		require.NoError(t, err)
		require.Equal(t, address, decoded)
	}
}

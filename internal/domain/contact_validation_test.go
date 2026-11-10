package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestContactDetailsValidation(t *testing.T) {
	for _, email := range []string{"bad", "Name <user@example.com>", "user@example.com\nBcc:x@example.com", "user@"} {
		_, err := (UpdateContactRequest{WorkEmail: &email}).Normalize()
		require.Error(t, err, email)
	}
	for _, phone := range []string{"87011234567", "+012345678", "+123", "+1234567890123456", "+7701abc0000"} {
		_, err := (UpdateContactRequest{Phone: &phone}).Normalize()
		require.Error(t, err, phone)
	}
	email, phone := " work@example.com ", " +7 (701) 123-45-67 "
	normalized, err := (UpdateContactRequest{WorkEmail: &email, Phone: &phone}).Normalize()
	require.NoError(t, err)
	require.Equal(t, "work@example.com", *normalized.WorkEmail)
	require.Equal(t, "+77011234567", *normalized.Phone)
	empty := " "
	normalized, err = (UpdateContactRequest{Phone: &empty}).Normalize()
	require.NoError(t, err)
	require.Nil(t, normalized.WorkEmail)
	require.Equal(t, "", *normalized.Phone)
}

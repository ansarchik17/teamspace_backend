package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNormalizeBoardMemberRole(t *testing.T) {
	for _, role := range []string{"viewer", "editor", "admin", "member"} {
		got, err := normalizeBoardMemberRole(role)
		require.NoError(t, err)
		require.Equal(t, role, got)
	}
	got, err := normalizeBoardMemberRole(" Admin ")
	require.NoError(t, err)
	require.Equal(t, "admin", got)
	for _, role := range []string{"", "owner", "superadmin", "administrator"} {
		_, err := normalizeBoardMemberRole(role)
		require.Error(t, err)
	}
}

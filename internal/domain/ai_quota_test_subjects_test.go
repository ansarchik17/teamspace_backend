package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAIQuotaTestSubjectsScope(t *testing.T) {
	org, otherOrg := uuid.New(), uuid.New()
	user, otherUser := int64(2), int64(3)
	entry := "2@" + org.String()
	subjects, err := ParseAIQuotaTestSubjects(" " + entry + "," + entry + " ")
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	for _, tc := range []struct {
		name    string
		org     uuid.UUID
		user    *int64
		allowed bool
	}{
		{"selected user", org, &user, true},
		{"selected company service", org, nil, true},
		{"colleague remains enforced", org, &otherUser, false},
		{"transferred user remains enforced", otherOrg, &user, false},
		{"other company service", otherOrg, nil, false},
		{"personal account", uuid.Nil, &user, false},
		{"no identity", uuid.Nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.allowed, subjects.Allows(tc.org, tc.user)) })
	}
	require.False(t, AIQuotaTestSubjects(nil).Allows(org, &user))
}

func TestAIQuotaTestSubjectsRejectBroadOrMalformedEntries(t *testing.T) {
	org := uuid.NewString()
	for _, value := range []string{"*", "2", "@" + org, "*@" + org, "0@" + org, "-2@" + org, "2@*", "2@" + uuid.Nil.String(), "2@invalid", "2@" + org + ",", "2@" + org + "@extra"} {
		t.Run(value, func(t *testing.T) {
			subjects, err := ParseAIQuotaTestSubjects(value)
			require.Error(t, err)
			require.Empty(t, subjects)
		})
	}
	subjects, err := ParseAIQuotaTestSubjects("  ")
	require.NoError(t, err)
	require.Empty(t, subjects)
}

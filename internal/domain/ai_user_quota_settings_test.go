package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetAIUserQuotaValidation(t *testing.T) {
	zero := int64(0)
	max := MaxAIBudgetMicrocredits
	require.True(t, (SetAIUserQuotaRequest{&zero, &zero, &zero, &zero}).Valid())
	require.True(t, (SetAIUserQuotaRequest{&max, &max, &zero, &zero}).Valid())
	for _, bad := range []*int64{nil, creditTokens(-1), creditTokens(max + 1)} {
		require.False(t, (SetAIUserQuotaRequest{bad, &zero, &zero, &zero}).Valid())
		require.False(t, (SetAIUserQuotaRequest{&zero, bad, &zero, &zero}).Valid())
	}
	for _, bad := range []*int64{nil, creditTokens(-1)} {
		require.False(t, (SetAIUserQuotaRequest{&zero, &zero, bad, &zero}).Valid())
		require.False(t, (SetAIUserQuotaRequest{&zero, &zero, &zero, bad}).Valid())
	}
}

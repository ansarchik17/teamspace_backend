package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetAIBudgetRequestValidation(t *testing.T) {
	v := int64(0)
	max := MaxAIBudgetMicrocredits
	req := SetAIBudgetRequest{&v, &v, &v, &v}
	require.True(t, req.Valid())
	require.True(t, (SetAIBudgetRequest{&max, &max, &max, &v}).Valid())
	for _, bad := range []*int64{nil, creditTokens(-1), creditTokens(max + 1)} {
		require.False(t, (SetAIBudgetRequest{bad, &v, &v, &v}).Valid())
		require.False(t, (SetAIBudgetRequest{&v, bad, &v, &v}).Valid())
		if bad != nil {
			require.False(t, (SetAIBudgetRequest{&v, &v, bad, &v}).Valid())
		}
	}
	require.True(t, (SetAIBudgetRequest{WeeklyBudget: &v, MaxFiveHour: &v, Revision: &v}).Valid())
	require.False(t, (SetAIBudgetRequest{&v, &v, &v, nil}).Valid())
	require.False(t, (SetAIBudgetRequest{&v, &v, &v, creditTokens(-1)}).Valid())
}

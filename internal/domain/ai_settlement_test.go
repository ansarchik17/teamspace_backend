package domain

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAICreditPersistedRates(t *testing.T) {
	i, o, total, cached, reasoning := int64(5), int64(3), int64(8), int64(2), int64(1)
	u := AICreditUsage{Status: "derived", InputTokens: &i, OutputTokens: &o, TotalTokens: &total, CachedInput: &cached, ReasoningOutput: &reasoning}
	rates := AICreditRates{Input: 7, Output: 11, Cached: 2, Reasoning: 13}
	amount, err := CalculateAICreditsAtRates(rates, u)
	require.NoError(t, err)
	require.Equal(t, int64(60), amount)
	rates.Input = math.MaxInt64
	_, err = CalculateAICreditsAtRates(rates, u)
	require.ErrorIs(t, err, ErrAICreditOverflow)
	_, err = CalculateAICreditsAtRates(AICreditRates{Input: 1}, u)
	require.ErrorIs(t, err, ErrAICreditInvalidUsage)
}

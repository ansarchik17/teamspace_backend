package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAIAudioCreditsUseExplicitUnits(t *testing.T) {
	quantity := int64(60)
	key := AICreditModelKey{ProviderID: "nitec", EndpointKind: "stt", ModelID: "openai/whisper-large-v3-turbo", PolicyVersion: AIAudioCreditPolicyV1}
	usage := AICreditUsage{Status: "derived", MeasuredUnit: "audio_seconds", MeasuredQuantity: &quantity}
	result, err := CalculateAICredits(key, usage)
	require.NoError(t, err)
	require.EqualValues(t, 1_200_000, result.Microcredits)
	usage.InputTokens, usage.OutputTokens = &quantity, &quantity
	_, err = CalculateAICredits(key, usage)
	require.ErrorIs(t, err, ErrAICreditInvalidUsage)
	usage.InputTokens, usage.OutputTokens = nil, nil
	usage.MeasuredUnit = "text_characters"
	_, err = CalculateAICredits(key, usage)
	require.ErrorIs(t, err, ErrAICreditUnknownUsage)
	key.PolicyVersion = AICreditPolicyV1
	_, err = CalculateAICredits(key, usage)
	require.ErrorIs(t, err, ErrAICreditUnknownModel)
}

func TestAITTSCountsUnicodeCharacters(t *testing.T) {
	quantity := int64(1000)
	key := AICreditModelKey{ProviderID: "elevenlabs", EndpointKind: "tts", ModelID: "eleven_multilingual_v2", PolicyVersion: AIAudioCreditPolicyV1}
	result, err := CalculateAICredits(key, AICreditUsage{Status: "derived", MeasuredUnit: "text_characters", MeasuredQuantity: &quantity})
	require.NoError(t, err)
	require.EqualValues(t, 1_000_000, result.Microcredits)
	quantity = -1
	_, err = CalculateAICredits(key, AICreditUsage{Status: "derived", MeasuredUnit: "text_characters", MeasuredQuantity: &quantity})
	require.ErrorIs(t, err, ErrAICreditInvalidUsage)
}

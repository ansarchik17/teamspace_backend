package domain

// These are product quota weights, not provider prices or monetary charges.
// Keep the version immutable: 20,000 microcredits/started audio second (1.2
// credits/minute), 1,000/submitted Unicode character (1 credit/1,000 chars).
func aiAudioRates(key AICreditModelKey) (AICreditRates, error) {
	if key.EndpointKind == "stt" {
		allowed := (key.ProviderID == "nitec" && (key.ModelID == "openai/whisper-large-v3" || key.ModelID == "openai/whisper-large-v3-turbo")) ||
			(key.ProviderID == "openai-direct" && (key.ModelID == "gpt-4o-transcribe" || key.ModelID == "gpt-4o-mini-transcribe" || key.ModelID == "whisper-1")) ||
			(key.ProviderID == "elevenlabs" && (key.ModelID == "scribe_v1" || key.ModelID == "scribe_v2"))
		if allowed {
			return AICreditRates{Input: 20_000, InputOnly: true, Unit: "audio_seconds"}, nil
		}
	}
	if key.EndpointKind == "tts" {
		allowed := ((key.ProviderID == "nitec" || key.ProviderID == "openai-direct") && (key.ModelID == "tts-1" || key.ModelID == "tts-1-hd" || key.ModelID == "gpt-4o-mini-tts")) ||
			(key.ProviderID == "nitec" && key.ModelID == "Qwen/Qwen3-TTS-12Hz-1.7B-Base") ||
			(key.ProviderID == "elevenlabs" && (key.ModelID == "eleven_multilingual_v2" || key.ModelID == "eleven_v3" || key.ModelID == "eleven_turbo_v2_5" || key.ModelID == "eleven_flash_v2_5"))
		if allowed {
			return AICreditRates{Input: 1_000, InputOnly: true, Unit: "text_characters"}, nil
		}
	}
	return AICreditRates{}, ErrAICreditUnknownModel
}

func (u AICreditUsage) HasKnownCounters() bool {
	return (u.Status == "verified" || u.Status == "derived") &&
		((u.InputTokens != nil && u.OutputTokens != nil) || (u.MeasuredUnit != "" && u.MeasuredQuantity != nil))
}

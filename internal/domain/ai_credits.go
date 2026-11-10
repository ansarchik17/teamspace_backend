package domain

import (
	"errors"
	"math"
)

const (
	MicrocreditsPerCredit         int64 = 1_000_000
	AICreditPolicyV1                    = "credits-v1"
	AIAudioCreditPolicyV1               = "audio-credits-v1"
	DefaultAIFiveHourMicrocredits int64 = 20_000 * MicrocreditsPerCredit
	DefaultAIWeeklyMicrocredits   int64 = 100_000 * MicrocreditsPerCredit
)

var (
	ErrAICreditUnknownPolicy = errors.New("unknown AI credit policy")
	ErrAICreditUnknownModel  = errors.New("unknown AI credit provider, endpoint or model")
	ErrAICreditUnknownUsage  = errors.New("AI usage is not verified or is incomplete")
	ErrAICreditInvalidUsage  = errors.New("invalid AI token usage")
	ErrAICreditOverflow      = errors.New("AI credit calculation overflows int64")
)

// AICreditModelKey is exact and case-sensitive. No inferred aliases or fallback.
type AICreditModelKey struct {
	ProviderID    string
	EndpointKind  string
	ModelID       string
	PolicyVersion string
}

// AICreditRates is a value snapshot in integer microcredits per token.
// InputOnly means that Output must be an explicitly verified zero.
type AICreditRates struct {
	Input     int64
	Output    int64
	Cached    int64
	Reasoning int64
	InputOnly bool
	Unit      string
}

// AICreditUsage contains normalized counters, not a provider response DTO.
// Status must be verified or derived by a trusted usage adapter, including for
// zero usage. Missing counters must not be converted to pointers to zero.
type AICreditUsage struct {
	Status           string
	InputTokens      *int64
	OutputTokens     *int64
	TotalTokens      *int64
	CachedInput      *int64
	ReasoningOutput  *int64
	MeasuredUnit     string `json:"measured_unit,omitempty"`
	MeasuredQuantity *int64 `json:"measured_quantity,omitempty"`
}

type AICreditCalculation struct {
	ModelKey     AICreditModelKey
	Rates        AICreditRates
	Microcredits int64
}

// AICreditRatesForModel returns the approved v1 product weights, not provider
// prices or authorization to call a model. Embeddings/rerank still require a
// separately confirmed usage contract before enforcement can enable them.
func AICreditRatesForModel(key AICreditModelKey) (AICreditRates, error) {
	if key.PolicyVersion == AIAudioCreditPolicyV1 {
		return aiAudioRates(key)
	}
	if key.PolicyVersion != AICreditPolicyV1 {
		return AICreditRates{}, ErrAICreditUnknownPolicy
	}
	if key.ProviderID == "nitec" {
		switch key.EndpointKind {
		case "chat":
			switch key.ModelID {
			case "openai/gpt-oss-120b", "google/gemma-3-27b", "Qwen/Qwen3-8B",
				"alemgpt-intent", "astanahub/alemllm", "meta-llama/Llama-4-Maverick-17B-128E-Instruct":
				return aiChatRates(1000, 3000), nil
			case "zai-org/GLM-4.6", "moonshotai/Kimi-K2-Thinking", "deepseek-ai/DeepSeek-V3.2",
				"Qwen/Qwen3.5-397B-A17B", "Qwen/Qwen3-VL-235B-A22B-Instruct", "deepseek-ai/DeepSeek-OCR":
				return aiChatRates(2000, 6000), nil
			}
		case "embeddings":
			if key.ModelID == "BAAI/bge-m3" {
				return AICreditRates{Input: 100, Cached: 100, InputOnly: true}, nil
			}
		case "rerank":
			if key.ModelID == "BAAI/bge-reranker-v2-m3" {
				return AICreditRates{Input: 200, Cached: 200, InputOnly: true}, nil
			}
		}
	}
	if key.ProviderID == "openai-direct" && key.EndpointKind == "chat" && key.ModelID == "gpt-4o" {
		return aiChatRates(2000, 6000), nil
	}
	return AICreditRates{}, ErrAICreditUnknownModel
}

func aiChatRates(input, output int64) AICreditRates {
	return AICreditRates{Input: input, Output: output, Cached: input, Reasoning: output}
}

// CalculateAICredits performs pure integer arithmetic. It neither reserves nor
// charges a budget. On error it returns no partial calculation; the caller must
// not treat the zero result as a free operation.
func CalculateAICredits(key AICreditModelKey, usage AICreditUsage) (AICreditCalculation, error) {
	rates, err := AICreditRatesForModel(key)
	if err != nil {
		return AICreditCalculation{}, err
	}
	charge, err := calculateAICreditAmount(rates, usage)
	if err != nil {
		return AICreditCalculation{}, err
	}
	return AICreditCalculation{ModelKey: key, Rates: rates, Microcredits: charge}, nil
}

// CalculateAICreditsAtRates replays a persisted immutable price snapshot. Rates
// are trusted server data, never supplied by an HTTP client.
func CalculateAICreditsAtRates(rates AICreditRates, usage AICreditUsage) (int64, error) {
	if rates.Input <= 0 || rates.Output < 0 || rates.Cached < 0 || rates.Reasoning < 0 || (!rates.InputOnly && rates.Output == 0) {
		return 0, ErrAICreditInvalidUsage
	}
	return calculateAICreditAmount(rates, usage)
}

func calculateAICreditAmount(rates AICreditRates, usage AICreditUsage) (int64, error) {
	if usage.Status != "verified" && usage.Status != "derived" {
		return 0, ErrAICreditUnknownUsage
	}
	if rates.Unit != "" {
		if usage.MeasuredUnit != rates.Unit || usage.MeasuredQuantity == nil {
			return 0, ErrAICreditUnknownUsage
		}
		if usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil || usage.CachedInput != nil || usage.ReasoningOutput != nil || *usage.MeasuredQuantity < 0 {
			return 0, ErrAICreditInvalidUsage
		}
		if rates.Input <= 0 || *usage.MeasuredQuantity > math.MaxInt64/rates.Input {
			return 0, ErrAICreditOverflow
		}
		return *usage.MeasuredQuantity * rates.Input, nil
	}
	if usage.MeasuredUnit != "" || usage.MeasuredQuantity != nil {
		return 0, ErrAICreditInvalidUsage
	}
	if usage.InputTokens == nil || usage.OutputTokens == nil {
		return 0, ErrAICreditUnknownUsage
	}
	for _, count := range []*int64{usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.CachedInput, usage.ReasoningOutput} {
		if count != nil && *count < 0 {
			return 0, ErrAICreditInvalidUsage
		}
	}
	input, output := *usage.InputTokens, *usage.OutputTokens
	if input > math.MaxInt64-output {
		return 0, ErrAICreditOverflow
	}
	if usage.TotalTokens != nil && *usage.TotalTokens != input+output {
		return 0, ErrAICreditInvalidUsage
	}
	var cached, reasoning int64
	if usage.CachedInput != nil {
		cached = *usage.CachedInput
	}
	if usage.ReasoningOutput != nil {
		reasoning = *usage.ReasoningOutput
	}
	if cached > input || reasoning > output || (rates.InputOnly && output != 0) {
		return 0, ErrAICreditInvalidUsage
	}
	// The validated subsets are subtracted, not charged again on top of totals.
	var charge int64
	for _, term := range []struct{ tokens, weight int64 }{
		{input - cached, rates.Input}, {cached, rates.Cached},
		{output - reasoning, rates.Output}, {reasoning, rates.Reasoning},
	} {
		if term.tokens == 0 {
			continue
		}
		if term.weight <= 0 {
			return 0, ErrAICreditInvalidUsage
		}
		if term.tokens > math.MaxInt64/term.weight {
			return 0, ErrAICreditOverflow
		}
		product := term.tokens * term.weight
		if charge > math.MaxInt64-product {
			return 0, ErrAICreditOverflow
		}
		charge += product
	}
	return charge, nil
}

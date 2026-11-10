package domain

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func creditTokens(n int64) *int64 { return &n }

func creditKey(model string) AICreditModelKey {
	return AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: model, PolicyVersion: AICreditPolicyV1}
}

func creditUsage(input, output int64) AICreditUsage {
	return AICreditUsage{Status: "verified", InputTokens: creditTokens(input), OutputTokens: creditTokens(output)}
}

func TestAICreditsApprovedModels(t *testing.T) {
	for _, group := range []struct {
		models []string
		input  int64
		output int64
		charge int64
	}{
		{[]string{"openai/gpt-oss-120b", "google/gemma-3-27b", "Qwen/Qwen3-8B", "alemgpt-intent", "astanahub/alemllm", "meta-llama/Llama-4-Maverick-17B-128E-Instruct"}, 1000, 3000, 5_000_000},
		{[]string{"zai-org/GLM-4.6", "moonshotai/Kimi-K2-Thinking", "deepseek-ai/DeepSeek-V3.2", "Qwen/Qwen3.5-397B-A17B", "Qwen/Qwen3-VL-235B-A22B-Instruct", "deepseek-ai/DeepSeek-OCR"}, 2000, 6000, 10_000_000},
	} {
		for _, model := range group.models {
			t.Run(model, func(t *testing.T) {
				key := creditKey(model)
				result, err := CalculateAICredits(key, creditUsage(2000, 1000))
				require.NoError(t, err)
				require.Equal(t, key, result.ModelKey)
				require.Equal(t, AICreditRates{Input: group.input, Output: group.output, Cached: group.input, Reasoning: group.output}, result.Rates)
				require.Equal(t, group.charge, result.Microcredits)
			})
		}
	}
	for _, tt := range []struct {
		key    AICreditModelKey
		input  int64
		output int64
		charge int64
	}{
		{AICreditModelKey{"openai-direct", "chat", "gpt-4o", AICreditPolicyV1}, 2000, 1000, 10_000_000},
		{AICreditModelKey{"nitec", "embeddings", "BAAI/bge-m3", AICreditPolicyV1}, 1234, 0, 123400},
		{AICreditModelKey{"nitec", "rerank", "BAAI/bge-reranker-v2-m3", AICreditPolicyV1}, 1234, 0, 246800},
	} {
		result, err := CalculateAICredits(tt.key, creditUsage(tt.input, tt.output))
		require.NoError(t, err)
		require.Equal(t, tt.charge, result.Microcredits)
	}
}

func TestAICreditsPrecisionAndSubsets(t *testing.T) {
	key := creditKey("openai/gpt-oss-120b")
	for _, tt := range []struct {
		name   string
		usage  AICreditUsage
		charge int64
	}{
		{"verified zero", creditUsage(0, 0), 0},
		{"one input token", creditUsage(1, 0), 1000},
		{"fractional credit is not rounded up", creditUsage(1, 1), 4000},
		{"only output", creditUsage(0, 10), 30000},
		{"total is not billed again", AICreditUsage{Status: "verified", InputTokens: creditTokens(2000), OutputTokens: creditTokens(1000), TotalTokens: creditTokens(3000)}, 5_000_000},
		{"subsets not extra tokens", AICreditUsage{Status: "verified", InputTokens: creditTokens(1000), OutputTokens: creditTokens(200), CachedInput: creditTokens(400), ReasoningOutput: creditTokens(50)}, 1_600_000},
		{"full subsets", AICreditUsage{Status: "verified", InputTokens: creditTokens(1000), OutputTokens: creditTokens(200), CachedInput: creditTokens(1000), ReasoningOutput: creditTokens(200)}, 1_600_000},
		{"already derived by adapter", AICreditUsage{Status: "derived", InputTokens: creditTokens(2), OutputTokens: creditTokens(1), TotalTokens: creditTokens(3)}, 5000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CalculateAICredits(key, tt.usage)
			require.NoError(t, err)
			require.Equal(t, tt.charge, result.Microcredits)
		})
	}
	first, err := CalculateAICredits(key, creditUsage(1, 1))
	require.NoError(t, err)
	second, err := CalculateAICredits(key, creditUsage(1, 1))
	require.NoError(t, err)
	require.Equal(t, int64(8000), first.Microcredits+second.Microcredits)
	// Caller modification of a returned value cannot mutate the policy registry.
	first.Rates.Input = 1
	rates, err := AICreditRatesForModel(key)
	require.NoError(t, err)
	require.Equal(t, int64(1000), rates.Input)
}

func TestAICreditsRejectUnknownModels(t *testing.T) {
	for _, key := range []AICreditModelKey{
		creditKey("unknown"), creditKey("openai/gpt-oss-120b-new"), creditKey("OPENAI/GPT-OSS-120B"), creditKey(" openai/gpt-oss-120b"),
		{"openai-direct", "chat", "openai/gpt-oss-120b", AICreditPolicyV1},
		{"nitec", "embeddings", "openai/gpt-oss-120b", AICreditPolicyV1},
		{"nitec", "chat", "BAAI/bge-m3", AICreditPolicyV1},
		{"unknown", "chat", "gpt-4o", AICreditPolicyV1},
	} {
		result, err := CalculateAICredits(key, creditUsage(0, 0))
		require.ErrorIs(t, err, ErrAICreditUnknownModel, "even zero usage cannot make an unknown model free")
		require.Equal(t, AICreditCalculation{}, result)
	}
	for _, version := range []string{"", "credits-v2"} {
		key := creditKey("openai/gpt-oss-120b")
		key.PolicyVersion = version
		_, err := CalculateAICredits(key, creditUsage(1, 1))
		require.ErrorIs(t, err, ErrAICreditUnknownPolicy)
	}
}

func TestAICreditsRejectInvalidUsage(t *testing.T) {
	for _, tt := range []struct {
		name  string
		usage AICreditUsage
		err   error
	}{
		{"absent usage", AICreditUsage{}, ErrAICreditUnknownUsage},
		{"unknown zero", AICreditUsage{Status: "unknown", InputTokens: creditTokens(0), OutputTokens: creditTokens(0)}, ErrAICreditUnknownUsage},
		{"unverified", AICreditUsage{Status: "invalid", InputTokens: creditTokens(1), OutputTokens: creditTokens(1)}, ErrAICreditUnknownUsage},
		{"only total", AICreditUsage{Status: "verified", TotalTokens: creditTokens(100)}, ErrAICreditUnknownUsage},
		{"do not infer output", AICreditUsage{Status: "verified", InputTokens: creditTokens(10), TotalTokens: creditTokens(100)}, ErrAICreditUnknownUsage},
		{"missing input", AICreditUsage{Status: "verified", OutputTokens: creditTokens(1)}, ErrAICreditUnknownUsage},
		{"negative input", creditUsage(-1, 0), ErrAICreditInvalidUsage},
		{"negative output", creditUsage(0, -1), ErrAICreditInvalidUsage},
		{"negative total", AICreditUsage{Status: "verified", InputTokens: creditTokens(0), OutputTokens: creditTokens(0), TotalTokens: creditTokens(-1)}, ErrAICreditInvalidUsage},
		{"negative cache", AICreditUsage{Status: "verified", InputTokens: creditTokens(1), OutputTokens: creditTokens(0), CachedInput: creditTokens(-1)}, ErrAICreditInvalidUsage},
		{"negative reasoning", AICreditUsage{Status: "verified", InputTokens: creditTokens(0), OutputTokens: creditTokens(1), ReasoningOutput: creditTokens(-1)}, ErrAICreditInvalidUsage},
		{"cache exceeds input", AICreditUsage{Status: "verified", InputTokens: creditTokens(1), OutputTokens: creditTokens(0), CachedInput: creditTokens(2)}, ErrAICreditInvalidUsage},
		{"reasoning exceeds output", AICreditUsage{Status: "verified", InputTokens: creditTokens(0), OutputTokens: creditTokens(1), ReasoningOutput: creditTokens(2)}, ErrAICreditInvalidUsage},
		{"inconsistent total", AICreditUsage{Status: "verified", InputTokens: creditTokens(2), OutputTokens: creditTokens(1), TotalTokens: creditTokens(4)}, ErrAICreditInvalidUsage},
		{"token sum overflow", creditUsage(math.MaxInt64, 1), ErrAICreditOverflow},
		{"input product overflow", creditUsage(math.MaxInt64/1000+1, 0), ErrAICreditOverflow},
		{"output product overflow", creditUsage(0, math.MaxInt64/3000+1), ErrAICreditOverflow},
		{"sum of valid products overflows", creditUsage(math.MaxInt64/1000, 1), ErrAICreditOverflow},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CalculateAICredits(creditKey("openai/gpt-oss-120b"), tt.usage)
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, AICreditCalculation{}, result, "no partially priced result on error")
		})
	}
	for _, key := range []AICreditModelKey{
		{"nitec", "embeddings", "BAAI/bge-m3", AICreditPolicyV1},
		{"nitec", "rerank", "BAAI/bge-reranker-v2-m3", AICreditPolicyV1},
	} {
		_, err := CalculateAICredits(key, creditUsage(1, 1))
		require.ErrorIs(t, err, ErrAICreditInvalidUsage)
	}
}

func TestAICreditsInt64Boundary(t *testing.T) {
	key := creditKey("openai/gpt-oss-120b")
	result, err := CalculateAICredits(key, creditUsage(math.MaxInt64/1000, 0))
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64/1000*1000), result.Microcredits)
	// Exercise exact boundary and subset arithmetic independently of v1 weights.
	amount, err := calculateAICreditAmount(aiChatRates(1, 1), creditUsage(math.MaxInt64, 0))
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), amount)
	usage := creditUsage(math.MaxInt64, 0)
	usage.CachedInput = creditTokens(math.MaxInt64)
	amount, err = calculateAICreditAmount(aiChatRates(1, 1), usage)
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), amount)
}

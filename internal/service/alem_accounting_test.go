package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func accountedAlemResponse(t *testing.T, model, usage string) *alem.ChatCompletionResponse {
	t.Helper()
	var resp alem.ChatCompletionResponse
	require.NoError(t, json.Unmarshal([]byte(`{"id":"provider-1","model":"`+model+`","choices":[{"message":{"role":"assistant","content":"answer"}}],"usage":`+usage+`}`), &resp))
	return &resp
}

func TestAlemChatUsageEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, model, usage string
		want               error
	}{
		{"valid", DefaultAlemModel, `{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}`, nil},
		{"null", DefaultAlemModel, `null`, domain.ErrAICreditUnknownUsage},
		{"missing input", DefaultAlemModel, `{"completion_tokens":4}`, domain.ErrAICreditUnknownUsage},
		{"missing output", DefaultAlemModel, `{"prompt_tokens":10}`, domain.ErrAICreditUnknownUsage},
		{"zero", DefaultAlemModel, `{"prompt_tokens":0,"completion_tokens":0}`, domain.ErrAICreditUnknownUsage},
		{"negative", DefaultAlemModel, `{"prompt_tokens":-1,"completion_tokens":4}`, domain.ErrAICreditInvalidUsage},
		{"total mismatch", DefaultAlemModel, `{"prompt_tokens":10,"completion_tokens":4,"total_tokens":15}`, domain.ErrAICreditInvalidUsage},
		{"bad details", DefaultAlemModel, `{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":11}}`, domain.ErrAICreditInvalidUsage},
		{"unknown model", "unknown", `{"prompt_tokens":10,"completion_tokens":4}`, domain.ErrAICreditUnknownModel},
		{"missing model", "", `{"prompt_tokens":10,"completion_tokens":4}`, domain.ErrAICreditUnknownModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := alemChatEvidence(accountedAlemResponse(t, tc.model, tc.usage))
			require.ErrorIs(t, err, tc.want)
			if tc.want != nil {
				require.NotEqual(t, "verified", evidence.Usage.Status)
			} else {
				require.Equal(t, "verified", evidence.Usage.Status)
				require.EqualValues(t, 2, *evidence.Usage.CachedInput)
				require.EqualValues(t, 1, *evidence.Usage.ReasoningOutput)
			}
		})
	}
	for _, usage := range []string{`{"prompt_tokens":1.5,"completion_tokens":1}`, `{"prompt_tokens":"2","completion_tokens":1}`, `{"prompt_tokens":9223372036854775808,"completion_tokens":1}`} {
		_, err := alemChatEvidence(&alem.ChatCompletionResponse{Model: DefaultAlemModel, AccountingUsage: json.RawMessage(usage)})
		require.ErrorIs(t, err, domain.ErrAICreditInvalidUsage)
	}
}

type alemPayerStub struct {
	user *domain.User
	err  error
}

func (s alemPayerStub) GetByID(context.Context, int64) (*domain.User, error) { return s.user, s.err }

func TestAlemChatPayerContext(t *testing.T) {
	org := uuid.New()
	s := &AlemService{paidCalls: NewAIPaidCallerService(&aiAccountingStub{}), payerUsers: alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}}
	ctx, err := s.paidUserContext(context.Background(), 42)
	require.NoError(t, err)
	caller, ok := domain.AIPaidCallerFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, org, caller.OrganizationID)
	require.EqualValues(t, 42, *caller.UserID)
	other, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
	_, err = s.paidUserContext(domain.WithAIPaidCaller(context.Background(), other), 42)
	require.ErrorIs(t, err, domain.ErrAIPaidCallContextMismatch)
	s.payerUsers = alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org}}
	_, err = s.paidUserContext(context.Background(), 42)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	s.payerUsers = nil
	_, err = s.paidUserContext(context.Background(), 42)
	require.ErrorIs(t, err, domain.ErrAIPaidCallMissingContext)
}

func TestAlemChatBlocksUnaccountedCalls(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	calls := 0
	client := &alemPaidChat{executor: NewAIPaidCallerService(store), client: completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return nil, nil
	}), turnID: uuid.NewString(), chatID: uuid.New()}
	req := alem.ChatCompletionRequest{Model: DefaultAlemModel, MaxTokens: 100, Messages: []alem.Message{{Role: "user", Content: "question"}}}
	_, err := client.CreateChatCompletion(context.Background(), req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallMissingContext)
	caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)
	req.Model = "unknown"
	_, err = client.CreateChatCompletion(ctx, req)
	require.ErrorIs(t, err, domain.ErrAICreditUnknownModel)
	req.Model = DefaultAlemModel
	req.Messages[0].Content = strings.Repeat("x", 512*1024)
	_, err = client.CreateChatCompletion(ctx, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidRequest)
	require.Zero(t, calls)
	require.Zero(t, store.reserveCalls)
}

func TestAlemChatInvalidUsageRetainsHold(t *testing.T) {
	for _, usage := range []string{`null`, `{"prompt_tokens":0,"completion_tokens":0}`, `{"prompt_tokens":2,"completion_tokens":1,"total_tokens":99}`} {
		store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
		caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
		client := &alemPaidChat{executor: NewAIPaidCallerService(store), client: completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			return accountedAlemResponse(t, DefaultAlemModel, usage), nil
		}), turnID: uuid.NewString(), chatID: uuid.New()}
		_, err := client.CreateChatCompletion(domain.WithAIPaidCaller(context.Background(), caller), alem.ChatCompletionRequest{Model: DefaultAlemModel, MaxTokens: 100, Messages: []alem.Message{{Role: "user", Content: "question"}}})
		require.Error(t, err)
		require.Empty(t, store.completed)
		require.Len(t, store.failures, 1)
		require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)
	}
}

func TestAlemChatLaterFailureKeepsPriorSettlements(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)
	lastErr := errors.New("last generation failed")
	step := 0
	client := &alemPaidChat{executor: NewAIPaidCallerService(store), client: completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		step++
		if step == 3 {
			return nil, lastErr
		}
		return accountedAlemResponse(t, DefaultAlemModel, `{"prompt_tokens":2,"completion_tokens":1}`), nil
	}), turnID: uuid.NewString(), chatID: uuid.New()}
	req := alem.ChatCompletionRequest{Model: DefaultAlemModel, MaxTokens: 100, Messages: []alem.Message{{Role: "user", Content: "question"}}}
	for i := 0; i < 2; i++ {
		_, err := client.CreateChatCompletion(ctx, req)
		require.NoError(t, err)
	}
	_, err := client.CreateChatCompletion(ctx, req)
	require.ErrorIs(t, err, lastErr)
	require.Len(t, store.completed, 2)
	require.Len(t, store.failures, 1)
	require.Equal(t, 3, store.reserveCalls)
}

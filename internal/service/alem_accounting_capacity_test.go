package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type chatCapacityStore struct {
	aiAccountingStub
	capacities []int64
	requests   []domain.AIReserveRequest
	shrinking  bool
	reason     string
}

func (s *chatCapacityStore) Reserve(ctx context.Context, req domain.AIReserveRequest) (domain.AIReservation, error) {
	s.requests = append(s.requests, req)
	if s.shrinking {
		available := req.Microcredits - 30_000
		return domain.AIReservation{}, &domain.AIReservationDenied{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", AvailableMicrocredits: &available}
	}
	for i, capacity := range s.capacities {
		if req.Microcredits > capacity {
			return domain.AIReservation{}, &domain.AIReservationDenied{
				Reason: s.denialReason(), Scope: []string{"company", "weekly", "five_hour"}[i], AvailableMicrocredits: &capacity,
			}
		}
	}
	return s.aiAccountingStub.Reserve(ctx, req)
}

func TestAlemChatFitsOutputToAvailableBudgetBeforeDispatch(t *testing.T) {
	for _, capacities := range [][]int64{{3_000_000}, {12_000_000, 8_000_000, 3_000_000}} {
		store := &chatCapacityStore{capacities: capacities}
		store.reservation = domain.AIReservation{AttemptID: uuid.New(), Created: true}
		caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
		calls := 0
		client := &alemPaidChat{executor: NewAIPaidCallerService(store), chatID: uuid.New(), turnID: uuid.NewString(),
			client: completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				calls++
				require.Positive(t, req.MaxTokens)
				require.Less(t, req.MaxTokens, 6000)
				wire, err := json.Marshal(req)
				require.NoError(t, err)
				accepted := store.requests[len(store.requests)-1]
				require.Equal(t, sha256.Sum256(wire), accepted.AttemptFingerprint)
				input, output := int64(len(wire)+512*len(req.Messages)), int64(req.MaxTokens)
				cost, err := domain.CalculateAICredits(accepted.Model, domain.AICreditUsage{Status: "derived", InputTokens: &input, OutputTokens: &output})
				require.NoError(t, err)
				require.Equal(t, cost.Microcredits, accepted.Microcredits)
				for _, limit := range capacities {
					require.LessOrEqual(t, cost.Microcredits, limit)
				}
				return accountedAlemResponse(t, DefaultAlemModel, `{"prompt_tokens":10,"completion_tokens":4}`), nil
			}),
		}
		_, err := client.CreateChatCompletion(domain.WithAIPaidCaller(context.Background(), caller), alem.ChatCompletionRequest{
			Model: DefaultAlemModel, MaxTokens: 6000, Messages: []alem.Message{{Role: "user", Content: "test"}},
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls, "only one paid request may be dispatched")
		require.Len(t, store.requests, len(capacities)+1)
		require.Len(t, store.completed, 1)
		for _, req := range store.requests {
			require.Equal(t, store.requests[0].IdempotencyKey, req.IdempotencyKey)
		}
	}
}

func TestAlemChatDoesNotRetryWhenInputCannotFit(t *testing.T) {
	for _, capacity := range []int64{0, 1} {
		store := &chatCapacityStore{capacities: []int64{capacity}}
		caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
		client := &alemPaidChat{executor: NewAIPaidCallerService(store), chatID: uuid.New(), turnID: uuid.NewString(),
			client: completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				t.Fatal("over-budget input must never be dispatched")
				return nil, nil
			}),
		}
		_, err := client.CreateChatCompletion(domain.WithAIPaidCaller(context.Background(), caller), alem.ChatCompletionRequest{
			Model: DefaultAlemModel, MaxTokens: 6000, Messages: []alem.Message{{Role: "user", Content: "test"}},
		})
		var denied *domain.AIReservationDenied
		require.ErrorAs(t, err, &denied)
		require.Len(t, store.requests, 1)
		require.Empty(t, store.completed)
	}
}

func TestAlemChatCapacityAdjustmentsAreBounded(t *testing.T) {
	store := &chatCapacityStore{shrinking: true}
	caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
	client := &alemPaidChat{executor: NewAIPaidCallerService(store), chatID: uuid.New(), turnID: uuid.NewString(),
		client: completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			t.Fatal("declined reservation must never be dispatched")
			return nil, nil
		}),
	}
	_, err := client.CreateChatCompletion(domain.WithAIPaidCaller(context.Background(), caller), alem.ChatCompletionRequest{
		Model: DefaultAlemModel, MaxTokens: 6000, Messages: []alem.Message{{Role: "user", Content: "test"}},
	})
	require.Error(t, err)
	require.Len(t, store.requests, 4, "concurrent capacity changes must not cause an unbounded retry loop")
}

func TestAlemChatCapacityHintAfterDispatchNeverRetries(t *testing.T) {
	store := &chatCapacityStore{capacities: []int64{3_000_000}}
	store.reservation = domain.AIReservation{AttemptID: uuid.New(), Created: true}
	caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
	calls := 0
	available := int64(2_000_000)
	providerErr := &domain.AIReservationDenied{Reason: "FIVE_HOUR_EXHAUSTED", Scope: "five_hour", AvailableMicrocredits: &available}
	client := &alemPaidChat{executor: NewAIPaidCallerService(store), chatID: uuid.New(), turnID: uuid.NewString(),
		client: completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			return nil, providerErr
		}),
	}
	_, err := client.CreateChatCompletion(domain.WithAIPaidCaller(context.Background(), caller), alem.ChatCompletionRequest{
		Model: DefaultAlemModel, MaxTokens: 6000, Messages: []alem.Message{{Role: "user", Content: "test"}},
	})
	require.ErrorIs(t, err, providerErr)
	require.Equal(t, 1, calls)
	require.Len(t, store.requests, 2)
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)
}

func (s *chatCapacityStore) denialReason() string {
	if s.reason != "" {
		return s.reason
	}
	return "RESERVATION_TOO_LARGE"
}

func TestAlemSmallRemainingBalanceIsUsableAndNotReportedAsRunning(t *testing.T) {
	for _, tc := range []struct {
		name, text   string
		capacity     int64
		wantPending  bool
		wantDispatch bool
	}{
		{"under 35 credits", "test", 3_000_000, false, true},
		{"eight percent with short input", "test", 38_350_000, false, true},
		{"eight percent with long history", strings.Repeat("long history ", 5000), 38_350_000, false, false},
		{"all remaining credits reserved", "test", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &chatCapacityStore{capacities: []int64{tc.capacity}, reason: "RESERVATION_PENDING"}
			store.reservation = domain.AIReservation{AttemptID: uuid.New(), Created: true}
			caller, _ := domain.NewAIPaidUserCaller(uuid.New(), 42)
			calls := 0
			client := &alemPaidChat{executor: NewAIPaidCallerService(store), chatID: uuid.New(), turnID: uuid.NewString(),
				client: completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
					calls++
					require.Positive(t, req.MaxTokens)
					require.LessOrEqual(t, store.requests[len(store.requests)-1].Microcredits, tc.capacity)
					return accountedAlemResponse(t, DefaultAlemModel, `{"prompt_tokens":10,"completion_tokens":4}`), nil
				}),
			}
			_, err := client.CreateChatCompletion(domain.WithAIPaidCaller(context.Background(), caller), alem.ChatCompletionRequest{
				Model: DefaultAlemModel, MaxTokens: 6000, Messages: []alem.Message{{Role: "user", Content: tc.text}},
			})
			if tc.wantDispatch {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				return
			}
			require.Zero(t, calls)
			var denied *domain.AIReservationDenied
			require.ErrorAs(t, err, &denied)
			if tc.wantPending {
				require.Equal(t, "RESERVATION_PENDING", denied.Reason)
			} else {
				require.Equal(t, "RESERVATION_TOO_LARGE", denied.Reason)
			}
			require.EqualValues(t, tc.capacity, *denied.AvailableMicrocredits)
			require.Nil(t, denied.RecheckAt)
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestPersonalAIJSONFallbackAcceptsValidatedNativeRead(t *testing.T) {
	reader := &fakePersonalReader{}
	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		switch calls {
		case 1:
			return nil, &alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Param: "tools"}}
		case 2:
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("native", `{"dataset":"tasks","params":{"mine":"true"}}`)}}), nil
		case 3:
			require.Empty(t, req.Tools)
			for _, msg := range req.Messages {
				require.NotEqual(t, "tool", msg.Role)
				require.Empty(t, msg.ToolCalls)
			}
			read := req.Messages[len(req.Messages)-2]
			_, err := decodeAIJSONReadStep(read, 0)
			require.NoError(t, err, "native call must become a complete JSON read envelope")
			require.Contains(t, req.Messages[len(req.Messages)-1].Content, `"type":"read_user_data_result"`)
			return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer_mode":"grounded","answer":"Review","card_ids":[]}`}), nil
		default:
			t.Fatal("compatible native read caused another completion")
			return nil, nil
		}
	})
	resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
	require.NoError(t, err)
	require.Equal(t, "Review", resp.Choices[0].Message.Content)
	require.Equal(t, 10, resp.Usage.TotalTokens)
	require.Equal(t, 3, calls)
	require.Len(t, reader.queries, 2)
}

func TestAIJSONNativeReadCompatibilityRejectsPrivilegedCommands(t *testing.T) {
	valid := personalTool("call", `{"dataset":"tasks"}`)
	for _, calls := range [][]alem.ToolCall{
		{personalTool("call", `{"dataset":"tasks","user_id":7}`)},
		{personalTool("call", `{"dataset":"tasks"} {"dataset":"mail"}`)},
		{personalTool("call", `{"dataset":"tasks","params":{"mine":true}}`)},
		{personalTool("", `{"dataset":"tasks"}`)},
		{valid, valid},
		{{ID: "call", Type: "function", Function: alem.ToolFunction{Name: "write_user_data", Arguments: `{"dataset":"tasks"}`}}},
	} {
		_, err := decodeAIJSONReadStep(alem.Message{Role: "assistant", ToolCalls: calls}, 0)
		require.Error(t, err)
	}
}

func TestPersonalAIRepairsCompletedProtocolOnce(t *testing.T) {
	for _, broken := range []alem.Message{
		{Role: "assistant", Content: nil},
		{Role: "assistant", Content: `{"action":"answer","answer":"private-response-marker","debug":"unsupported"}`},
		{Role: "assistant", Content: "private-response-marker"},
	} {
		reader := &fakePersonalReader{}
		calls := 0
		client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			if calls == 1 {
				return nil, &alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Param: "tools"}}
			}
			if calls == 2 {
				resp := personalResponse(broken)
				resp.Choices[0].FinishReason = "stop"
				return resp, nil
			}
			require.Equal(t, 3, calls)
			wire, err := json.Marshal(req)
			require.NoError(t, err)
			require.NotContains(t, string(wire), "private-response-marker")
			require.Contains(t, req.Messages[0].Content, "Предыдущий ответ не прошёл проверку формата")
			require.Zero(t, req.Temperature)
			return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer_mode":"general","answer":"Что вы хотите узнать?","card_ids":[]}`}), nil
		})
		resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: "123"}}})
		require.NoError(t, err)
		require.Equal(t, "Что вы хотите узнать?", resp.Choices[0].Message.Content)
		require.Equal(t, 10, resp.Usage.TotalTokens, "include the completed invalid response and correction")
		require.Len(t, reader.queries, 1, "general reply must not read business records")
		require.Equal(t, 3, calls)
	}
}

func TestAIProtocolCorrectionNeverRetriesIncompleteOrUntrustedOutput(t *testing.T) {
	for _, choice := range []alem.Choice{
		{FinishReason: "length", Message: alem.Message{Role: "assistant", Content: `{"action":"read","queries":[`}},
		{FinishReason: "", Message: alem.Message{Role: "assistant", Content: "invalid"}},
		{FinishReason: "content_filter", Message: alem.Message{Role: "assistant"}},
		{FinishReason: "stop", Message: alem.Message{Role: "user", Content: "invalid"}},
	} {
		repaired := false
		client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			t.Fatal("must not retry incomplete or untrusted output")
			return nil, nil
		})
		_, _, _ = completeAIJSONReadStep(context.Background(), client, alem.ChatCompletionRequest{}, &alem.ChatCompletionResponse{Choices: []alem.Choice{choice}}, 0, &repaired)
		require.False(t, repaired)
	}
}

func TestPersonalAIProtocolCorrectionBudgetIsSharedAcrossReadRounds(t *testing.T) {
	reader := &fakePersonalReader{}
	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			return nil, &alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Param: "tools"}}
		}
		resp := personalResponse(alem.Message{Role: "assistant", Content: "private-response-marker"})
		resp.Choices[0].FinishReason = "stop"
		if calls == 3 {
			resp.Choices[0].Message.Content = `{"action":"read","queries":[{"dataset":"tasks"}]}`
		}
		return resp, nil
	})
	_, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-response-marker")
	require.Contains(t, err.Error(), "round=1")
	require.Equal(t, 4, calls, "a later read round cannot receive another format correction")
	require.Len(t, reader.queries, 2)
}

func TestAIProtocolCorrectionPropagatesAccountingAndProviderErrors(t *testing.T) {
	for _, wantErr := range []error{
		&domain.AIReservationDenied{Reason: "RESERVATION_TOO_LARGE"},
		domain.ErrAIConcurrencyLimitExceeded,
		context.DeadlineExceeded,
		&alem.APIError{StatusCode: http.StatusServiceUnavailable},
	} {
		calls := 0
		client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			return nil, wantErr
		})
		repaired := false
		resp := personalResponse(alem.Message{Role: "assistant", Content: "bad format"})
		resp.Choices[0].FinishReason = "stop"
		_, _, err := completeAIJSONReadStep(context.Background(), client, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "system", Content: aiJSONReadProtocol}}}, resp, 0, &repaired)
		require.True(t, errors.Is(err, wantErr))
		require.Equal(t, 1, calls)
	}
}

type protocolReservationStore struct {
	*aiAccountingStub
	keys []string
}

func (s *protocolReservationStore) Reserve(ctx context.Context, req domain.AIReserveRequest) (domain.AIReservation, error) {
	s.keys = append(s.keys, req.IdempotencyKey)
	return s.aiAccountingStub.Reserve(ctx, req)
}

func TestPersonalAIProtocolCorrectionSettlesEachPaidAttempt(t *testing.T) {
	store := &protocolReservationStore{aiAccountingStub: &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}}
	caller, err := domain.NewAIPaidUserCaller(uuid.New(), 42)
	require.NoError(t, err)
	calls := 0
	client := &alemPaidChat{executor: NewAIPaidCallerService(store), turnID: "turn", chatID: uuid.New(), client: completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		resp := accountedAlemResponse(t, DefaultAlemModel, `{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}`)
		resp.Choices[0].FinishReason = "stop"
		resp.Choices[0].Message.Content = "private-response-marker"
		if calls == 3 {
			resp.Choices[0].Message.Content = `{"action":"answer","answer_mode":"general","answer":"Ответ","card_ids":[]}`
		}
		require.NotContains(t, strings.TrimSpace(req.Messages[0].Content.(string)), "private-response-marker")
		return resp, nil
	})}
	resp, err := completeWithUserData(domain.WithAIPaidCaller(context.Background(), caller), client, &fakePersonalReader{}, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Model: DefaultAlemModel})
	require.NoError(t, err)
	require.Equal(t, "Ответ", resp.Choices[0].Message.Content)
	require.Equal(t, 42, resp.Usage.TotalTokens)
	require.Equal(t, []string{"alem:turn:1", "alem:turn:2", "alem:turn:3"}, store.keys)
	require.Len(t, store.completed, 3)
	require.Empty(t, store.failures)
}

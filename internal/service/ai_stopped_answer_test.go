package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestPersonalAIOutputLimitPreservesPartialAnswerWithoutRetry(t *testing.T) {
	for _, content := range []string{
		`{"answer_mode":"general","answer":"Сохранённая часть`,
		`{"action":"answer","answer_mode":"general","answer":"Сохранённая часть`,
		"```json\r\n" + `{"answer_mode":"general","answer":"Сохранённая часть`,
		`{"answer_mode":"general","answer":"Сохранённая часть","card_ids":[]}`,
	} {
		reader := &fakePersonalReader{}
		calls := 0
		client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			resp := personalResponse(alem.Message{Role: "assistant", Content: content})
			resp.Choices[0].FinishReason = "length"
			return resp, nil
		})
		resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.Len(t, reader.queries, 1)
		require.Equal(t, "length", resp.Choices[0].FinishReason)
		require.Contains(t, resp.Choices[0].Message.Content, "Сохранённая часть")
		require.Contains(t, resp.Choices[0].Message.Content, "Ответ остановлен")
		require.Equal(t, 5, resp.Usage.TotalTokens)
	}
}

func TestPersonalAIOutputLimitNeverExecutesIncompleteCommands(t *testing.T) {
	for _, message := range []alem.Message{
		{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"private-marker`},
		{Role: "assistant", Content: `{"answer_mode":"grounded","answer":"private-marker`},
		{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("unfinished", `{"dataset":"private-marker`)}},
		{Role: "assistant", Content: nil},
	} {
		reader := &fakePersonalReader{}
		calls := 0
		client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			resp := personalResponse(message)
			resp.Choices[0].FinishReason = "length"
			return resp, nil
		})
		resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.Len(t, reader.queries, 1)
		require.NotContains(t, resp.Choices[0].Message.Content, "private-marker")
		require.Empty(t, resp.Choices[0].Message.ToolCalls)
	}
}

func TestPersonalAIBudgetStopsBetweenStepsWithoutRestoringUnsentDraft(t *testing.T) {
	reader := &fakePersonalReader{}
	calls := 0
	available := int64(1)
	client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("read", `{"dataset":"tasks"}`)}}), nil
		}
		return nil, &domain.AIReservationDenied{Reason: "RESERVATION_TOO_LARGE", Scope: "five_hour", AvailableMicrocredits: &available}
	})
	resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
	require.NoError(t, err, "already executed work must return a stopped result rather than a pre-call rejection")
	require.Equal(t, 2, calls)
	require.Equal(t, 5, resp.Usage.TotalTokens)
	require.Contains(t, resp.Choices[0].Message.Content, "пятичасового")
	require.Len(t, resp.DataSources, 1)
	require.Empty(t, resp.Choices[0].Message.ToolCalls)
	require.False(t, strings.Contains(resp.Choices[0].Message.Content.(string), "Сообщение не отправлено"))
}

func TestPersonalAIBusyAfterPaidStepReturnsStoppedResult(t *testing.T) {
	for _, denial := range []error{domain.ErrAIConcurrencyLimitExceeded, domain.ErrAIRateLimitExceeded, &domain.AIReservationDenied{Reason: "RESERVATION_PENDING", Scope: "account"}} {
		calls := 0
		client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			if calls == 1 {
				return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("read", `{"dataset":"tasks"}`)}}), nil
			}
			return nil, denial
		})
		resp, err := completeWithUserData(context.Background(), client, &fakePersonalReader{}, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
		require.NoError(t, err, "already dispatched work is not an unsent request")
		require.Equal(t, 2, calls)
		require.Equal(t, 5, resp.Usage.TotalTokens)
		require.Contains(t, resp.Choices[0].Message.Content, "следующий шаг сейчас недоступен")
		require.Len(t, resp.DataSources, 1)
	}
}

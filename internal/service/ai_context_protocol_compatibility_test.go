package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestPersonalAIAnswerEnvelopeCompatibility(t *testing.T) {
	native := `{"answer_mode":"general","answer":"Я на связи. Что проверить?","card_ids":[]}`
	withAction := `{"action":"answer","answer_mode":"general","answer":"Я на связи. Что проверить?","card_ids":[]}`
	for _, fallback := range []bool{false, true} {
		for _, envelope := range []string{native, withAction, "```json\r\n" + native + "\r\n```", "```\n" + withAction + "\n```"} {
			reader := &fakePersonalReader{}
			calls := 0
			client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				calls++
				if fallback && calls == 1 {
					return nil, &alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Param: "tools"}}
				}
				return personalResponse(alem.Message{Role: "assistant", Content: envelope}), nil
			})
			resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{
				Messages: []alem.Message{{Role: "user", Content: "проверка"}},
			})
			require.NoError(t, err)
			require.Equal(t, "Я на связи. Что проверить?", resp.Choices[0].Message.Content)
			require.Empty(t, resp.DataSources)
			require.Empty(t, resp.DataCards)
			require.Len(t, reader.queries, 1, "general chat must not read business data")
			wantCalls := 1
			if fallback {
				wantCalls = 2
			}
			require.Equal(t, wantCalls, calls, "compatible envelopes must not trigger another paid call")
		}
	}
}

func TestAIProtocolCompatibilityDoesNotInferCommands(t *testing.T) {
	for _, content := range []string{
		`{"queries":[{"dataset":"tasks"}]}`,
		`{"action":"write","answer":"untrusted"}`,
		`{"answer_mode":"general","answer":"untrusted","queries":[{"dataset":"tasks"}]}`,
		`{"answer_mode":"general","answer":"untrusted","card_ids":["S1-C1"]}`,
		`{"answer_mode":"general","answer":"untrusted","debug":"private-response-marker"}`,
		"Explanation\n```json\n{\"action\":\"read\",\"queries\":[{\"dataset\":\"tasks\"}]}\n```",
		`{"answer_mode":"general","answer":"first"} {"answer":"second"}`,
	} {
		_, err := decodeAIJSONReadStep(alem.Message{Role: "assistant", Content: content}, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "protocol:")
		require.NotContains(t, err.Error(), "private-response-marker")
	}
	for _, role := range []string{"user", "tool", "system"} {
		message := alem.Message{Role: role, Content: `{"action":"answer","answer_mode":"general","answer":"untrusted"}`}
		require.Empty(t, decodeAIDataAnswer(message).AnswerMode)
		_, err := decodeAIJSONReadStep(message, 0)
		require.Error(t, err)
	}
	message := decodeAIDataAnswer(alem.Message{Role: "assistant", Content: `{"action":"read","answer_mode":"general","answer":"untrusted"}`})
	require.Empty(t, message.AnswerMode)
}

func TestPersonalAICompatibleEnvelopeStillRequiresEvidence(t *testing.T) {
	reader := &fakePersonalReader{}
	calls := 0
	client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return personalResponse(alem.Message{Role: "assistant", Content: `{"answer_mode":"grounded","answer":"У вас три задачи","card_ids":[]}`}), nil
	})
	_, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{
		Messages: []alem.Message{{Role: "user", Content: "Мои задачи"}},
	})
	require.Error(t, err)
	require.Equal(t, 2, calls)
	require.Len(t, reader.queries, 1)
}

func TestPersonalAIProtocolDiagnosticsDoNotExposeResponse(t *testing.T) {
	reader := &fakePersonalReader{}
	calls := 0
	client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			return nil, &alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Param: "tools"}}
		}
		response := personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer":"private-response-marker`})
		response.Choices[0].FinishReason = "stop"
		return response, nil
	})
	_, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "protocol: invalid_json_schema"))
	require.Contains(t, err.Error(), "output_limit_reached=false")
	require.NotContains(t, err.Error(), "private-response-marker")
	require.Equal(t, 3, calls, "completed invalid output gets at most one accounted format correction")
}

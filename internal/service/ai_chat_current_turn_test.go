package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestMainChatReadQuestionDoesNotResumeOldCancellation(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	store := chatMemoryActions{}
	agent := &SmartAgentService{actionStore: store}
	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		for _, tool := range req.Tools {
			_, action := mainChatActionContracts[tool.Function.Name]
			require.False(t, action, "read request advertised a write")
		}
		if calls == 1 {
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("cancel_calendar_event", `{"event_id":"`+uuid.NewString()+`","event_query":"Обзор"}`)}}), nil
		}
		if calls == 2 {
			require.Contains(t, req.Messages[len(req.Messages)-1].Content, "current_turn_read_only")
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("read-tasks", `{"dataset":"tasks","params":{"mine":"true"}}`)}}), nil
		}
		return personalResponse(alem.Message{Role: "assistant", Content: `{"answer_mode":"grounded","answer":"У вас задача Review.","card_ids":[]}`}), nil
	})
	resp, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: []alem.Message{
		{Role: "user", Content: "Отмени встречу Обзор"},
		{Role: "assistant", Content: "Уточните какую встречу отменить"},
		{Role: "user", Content: "Какие у меня задачи на этой неделе?"},
	}}, agent)
	require.NoError(t, err)
	require.Contains(t, resp.Choices[0].Message.Content, "Review")
	require.Len(t, resp.DataSources, 1)
	require.Equal(t, "tasks", resp.DataSources[0].Dataset)
	require.Empty(t, store)
	require.Equal(t, 3, calls)
}

func TestMainChatCancellationRepairsReferenceWithoutRepeatedQuestion(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "json"}[jsonMode], func(t *testing.T) {
			ctx, identity := chatTestIdentity(t)
			store := chatMemoryActions{}
			agent := &SmartAgentService{actionStore: store}
			event := uuid.NewString()
			calls := 0
			client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				if jsonMode && req.Tools != nil {
					return nil, &alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Param: "tools"}}
				}
				calls++
				args := `{"event_id":"` + event + `"}`
				if calls > 1 {
					require.Contains(t, req.Messages[len(req.Messages)-1].Content, "action_target_resolution_required")
					args = `{"event_id":"` + event + `","event_query":"завтра в 10 утра"}`
				}
				if jsonMode {
					return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"cancel_calendar_event","arguments":` + args + `}`}), nil
				}
				return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("cancel_calendar_event", args)}}), nil
			})
			resp, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: []alem.Message{
				{Role: "user", Content: "Какие у меня встречи за эту неделю?"},
				{Role: "assistant", Content: "Встреча с t.company 24 октября в 10 утра"},
				{Role: "user", Content: "отмени встречю которая на завтра в 10 утра"},
			}}, agent)
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Len(t, store, 1)
			require.Len(t, resp.DataCards, 1)
			require.NotNil(t, resp.DataCards[0].Confirmation)
			require.Empty(t, resp.DataSources, "internal repair is not a checked data source")
			require.NotContains(t, resp.Choices[0].Message.Content, "Отменяю")
		})
	}
}

func TestMainChatCurrentTurnReadOnlyClassification(t *testing.T) {
	for _, question := range []string{"Какие у меня встречи за эту неделю?", "Сколько у меня лидов?", "Покажи список задач", "Show my meetings"} {
		require.True(t, chatCurrentTurnReadOnly(question), question)
	}
	for _, question := range []string{"Отмени встречу завтра в 10 утра", "Найди встречу и отмени её", "Какие у меня встречи? Отмени первую", "Напиши ему алё", "да", "встречу с t.company"} {
		require.False(t, chatCurrentTurnReadOnly(question), question)
	}
	for _, tool := range mainChatToolSchemas() {
		if chatTargetResolutionTool(tool.Function.Name) {
			raw := tool.Function.Parameters["properties"].(map[string]any)
			key := "event_query"
			if tool.Function.Name == "close_kanban_task" {
				key = "task_query"
			}
			require.Contains(t, strings.ToLower(raw[key].(map[string]string)["description"]), "даже когда id известен")
		}
	}
}

func TestMainChatTargetRepairIsBounded(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	store := chatMemoryActions{}
	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("cancel_calendar_event", `{}`)}}), nil
	})
	response, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: "Отмени встречу завтра в 10 утра"}}}, &SmartAgentService{actionStore: store})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.Contains(t, response.Choices[0].Message.Content, "уточните")
	require.Empty(t, store)
	require.Empty(t, response.DataSources)
}

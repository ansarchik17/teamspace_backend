package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestMainChatTaskRequiresUserDetailsBeforeConfirmation(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	store := chatMemoryActions{}
	agent := &SmartAgentService{actionStore: store}
	for _, raw := range []string{
		`{"title":"Task for t.company","priority":"medium","assignee_ids":[38],"participant_query":"t.company"}`,
		`{"title":"Task for t.company","priority":"medium","assignee_ids":[38],"participant_query":"t.company","due_at":"2026-10-24T18:00:00+05:00"}`,
		`{}`,
	} {
		result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall("create_kanban_task", raw), "создай задачу для t.company")
		require.Equal(t, "clarification_required", result.Status)
		require.Contains(t, result.Message, "название")
		require.Contains(t, result.Message, "приоритет")
		require.Contains(t, result.Message, "срок")
		require.False(t, pending)
		require.Empty(t, cards)
		require.Empty(t, store, "incomplete or invented fields must not create a confirmable action")
	}
}

func TestMainChatTaskLoopClarifiesThenCollectsDetailsInBothProtocols(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "json"}[jsonMode], func(t *testing.T) {
			ctx, identity := chatTestIdentity(t)
			store := chatMemoryActions{}
			agent := &SmartAgentService{actionStore: store}
			history := []alem.Message{{Role: "user", Content: "создай задачу для t.company"}}
			raw := `{"title":"Task for t.company","priority":"medium","assignee_ids":[38],"participant_query":"t.company"}`
			clientCalls := 0
			client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				clientCalls++
				if jsonMode && req.Tools != nil {
					return nil, &alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Param: "tools"}}
				}
				if jsonMode {
					return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"create_kanban_task","arguments":` + raw + `}`}), nil
				}
				return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("create_kanban_task", raw)}}), nil
			})
			resp, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: history}, agent)
			require.NoError(t, err)
			require.Empty(t, resp.DataCards)
			require.Empty(t, store)
			require.Contains(t, resp.Choices[0].Message.Content, "уточните")
			require.LessOrEqual(t, clientCalls, 2, "clarification must stop rather than spend more model calls inventing arguments")

			history = append(history, resp.Choices[0].Message, alem.Message{Role: "user", Content: "Название: Проверить договор. Приоритет высокий. Срок 2026-10-24."})
			raw = `{"title":"Проверить договор","priority":"high","assignee_ids":[38],"participant_query":"t.company","due_at":"2026-10-24T18:00:00+05:00"}`
			resp, err = completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: history}, agent)
			require.NoError(t, err)
			require.Len(t, store, 1)
			require.Len(t, resp.DataCards, 1)
			require.NotNil(t, resp.DataCards[0].Confirmation)
			serialized, _ := json.Marshal(resp.DataCards[0].Fields)
			require.Contains(t, string(serialized), "Проверить договор")
			require.Contains(t, string(serialized), "24.10.2026 23:59:59 (Asia/Almaty)")
		})
	}
}

func TestMainChatTaskIgnoresAssistantAndToolSuggestions(t *testing.T) {
	messages := []alem.Message{
		{Role: "user", Content: "создай задачу для t.company"},
		{Role: "assistant", Content: "Название Проверить договор, высокий приоритет, 2026-10-24"},
		{Role: "tool", Content: "Проверить договор высокий 2026-10-24"},
	}
	args := map[string]any{"title": "Проверить договор", "priority": "high", "due_at": "2026-10-24T18:00:00+05:00", "assignee_ids": []any{float64(38)}}
	require.Error(t, chatTaskUserFields(args, chatActionUserText(messages), time.Now()))
	args["title"] = "создай задачу"
	args["priority"] = "medium"
	require.Error(t, chatTaskUserFields(args, "создай задачу, срок 2026-10-24", time.Now()), "medium must not be accepted as an implicit default")
}

func TestMainChatTaskDateAndAssigneesValidation(t *testing.T) {
	for _, raw := range []string{
		`{"title":"Договор","priority":"high","due_at":"tomorrow","assignee_ids":[38],"participant_query":"t.company"}`,
		`{"title":"Договор","priority":"high","due_at":"2026-10-24T10:00:00","assignee_ids":[38],"participant_query":"t.company"}`,
		`{"title":"Договор","priority":"high","due_at":"2026-10-24T10:00:00+05:00","assignee_ids":[]}`,
		`{"title":"Договор","priority":"high","due_at":"2026-10-24T10:00:00+05:00","assignee_ids":[-1]}`,
	} {
		_, err := validateChatToolArguments("create_kanban_task", raw, mainChatToolSchemas())
		require.Error(t, err)
	}
	zone := time.FixedZone("Asia/Almaty", 5*3600)
	now := time.Date(2026, 10, 23, 23, 30, 0, 0, zone)
	due := now.AddDate(0, 0, 1)
	require.True(t, chatActionDateInUserText("срок завтра", due, now))
	require.False(t, chatActionDateInUserText("срок послезавтра", due, now))
	require.True(t, chatActionDateInUserText("к 24 октября", due, now))
	require.False(t, chatActionDateInUserText("к 24 октября 2027", due, now))
	require.False(t, chatActionDateInUserText("к 124 октября", due, now))
}

func TestMainChatTaskPriorityMustBeExplicitAndUseLatestChoice(t *testing.T) {
	require.False(t, chatTaskPriorityInUserText("Название: Высокий спрос", "high"))
	require.False(t, chatTaskPriorityInUserText("не высокий приоритет", "high"))
	require.False(t, chatTaskPriorityInUserText("приоритет высокий или средний", "high"))
	require.True(t, chatTaskPriorityInUserText("приоритет средний\nвысокий", "high"))
	require.False(t, chatTaskPriorityInUserText("приоритет средний\nвысокий", "medium"))
	require.True(t, chatTaskPriorityInUserText("Проверить договор, 2026-10-24, высокий", "high"))
}

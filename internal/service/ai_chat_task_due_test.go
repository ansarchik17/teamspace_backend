package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestMainChatTaskAcceptsDateAndWeekEndWithoutClock(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-23T04:16:00+05:00")
	for _, raw := range []string{"25.10.2026", "2026-10-25", "конец недели", "до конца недели"} {
		due, err := parseChatTaskDue(raw, now)
		require.NoError(t, err)
		require.Equal(t, "2026-10-25T23:59:59+05:00", due.Format(time.RFC3339))
	}
	sunday, _ := time.Parse(time.RFC3339, "2026-10-25T12:00:00+05:00")
	require.Equal(t, "2026-10-25T23:59:59+05:00", chatTaskWeekDeadline(sunday).Format(time.RFC3339))
	args := map[string]any{"title": "протестить фронт сайта", "priority": "high", "due_at": "2026-10-25T17:00:00+05:00", "assignee_ids": []any{float64(38)}, "participant_query": "Test Company"}
	require.NoError(t, chatActionUserFields("create_kanban_task", args, "создай задачу для Test Company\nзаголовок: протестить фронт сайта приоритет: высокий срок: конец недели", now))
	require.Equal(t, "2026-10-25T23:59:59+05:00", args["due_at"], "an unrequested model clock must become the displayed end-of-day policy")
	args["due_at"] = "2026-10-25T17:00:00+05:00"
	require.NoError(t, chatActionUserFields("create_kanban_task", args, "создай задачу для Test Company\nзаголовок: протестить фронт сайта приоритет: высокий срок: 25.10.2026 в 17:00", now))
	require.Equal(t, "2026-10-25T17:00:00+05:00", args["due_at"])
	args["due_at"] = "2026-10-25T18:00:00+05:00"
	require.Error(t, chatActionUserFields("create_kanban_task", args, "создай задачу для Test Company\nзаголовок: протестить фронт сайта приоритет: высокий срок: 25.10.2026 в 17:00", now), "a conflicting explicit clock must not become end of day")
	args["due_at"] = "2026-10-26T17:00:00+05:00"
	require.Error(t, chatActionUserFields("create_kanban_task", args, "создай задачу для Test Company\nзаголовок: протестить фронт сайта приоритет: высокий срок: конец недели", now))
}

func TestMainChatTaskRetainsAssigneeWhileDateIsClarified(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "json"}[jsonMode], func(t *testing.T) {
			ctx, identity := chatTestIdentity(t)
			store := chatMemoryActions{}
			reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/me" {
					io.WriteString(w, `{"id":42}`)
					return
				}
				require.Equal(t, "/api/v1/search/users", r.URL.Path)
				io.WriteString(w, `{"users":[{"id":38,"username":"t.company","first_name":"Test","last_name":"Company"}],"count":1}`)
			}), nil)
			calls := 0
			client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				if jsonMode && req.Tools != nil {
					return nil, &alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Param: "tools"}}
				}
				calls++
				if calls == 1 {
					if jsonMode {
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"colleague_search","params":{"q":"Test Company"}}]}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("directory", `{"dataset":"colleague_search","params":{"q":"Test Company"}}`)}}), nil
				}
				args := `{"title":"протестить фронт сайта","priority":"high","due_at":"2026-10-25","assignee_ids":[38]}`
				if jsonMode {
					return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"create_kanban_task","arguments":` + args + `}`}), nil
				}
				return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("create_kanban_task", args)}}), nil
			})
			response, err := completeWithUserData(ctx, client, reader, identity, alem.ChatCompletionRequest{Messages: []alem.Message{
				{Role: "user", Content: "создай задачу для Test Company"},
				{Role: "assistant", Content: "Укажите название, приоритет и срок"},
				{Role: "user", Content: "заголовок: протестить фронт сайта приоритет: высокий срок: конец недели"},
				{Role: "assistant", Content: "Уточните дату"},
				{Role: "user", Content: "25.10.2026"},
			}}, &SmartAgentService{actionStore: store})
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Len(t, store, 1)
			require.NotNil(t, response.DataCards[0].Confirmation)
			for _, raw := range store {
				var pending aiPendingChatAction
				require.NoError(t, json.Unmarshal([]byte(raw), &pending))
				require.Contains(t, pending.Arguments, "Test Company")
				require.Contains(t, pending.Arguments, "2026-10-25T23:59:59+05:00")
				require.Contains(t, pending.Arguments, "протестить фронт сайта")
			}
			values := map[string]string{}
			for _, field := range response.DataCards[0].Fields {
				values[field.Label] = field.Value
			}
			require.Equal(t, "25.10.2026 23:59:59 (Asia/Almaty)", values["Срок"])
			require.Equal(t, "Test Company", values["Кому"])
		})
	}
}

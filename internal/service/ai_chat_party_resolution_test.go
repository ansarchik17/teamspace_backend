package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
)

func partyEvidence(dataset string, data string) aiReadEvidence {
	return aiReadEvidence{Source: alem.DataSource{Dataset: dataset, Status: "ok"}, Result: json.RawMessage(`{"status":"ok","data":` + data + `}`)}
}

func TestMainChatBindsRecipientOnlyFromVerifiedDirectoryAlias(t *testing.T) {
	source := partyEvidence("colleague_search", `{"users":[{"id":38,"username":"t.company","first_name":"Test","last_name":"Company","email":"t.company@a.teamspace.kz"}],"count":1}`)
	for _, args := range []string{
		`{"title":"Отчет","priority":"high","due_at":"2026-10-24T23:59:59+05:00","assignee_ids":[38]}`,
		`{"title":"Отчет","priority":"high","due_at":"2026-10-24T23:59:59+05:00","assignee_ids":[38],"participant_query":"Test Company"}`,
		`{"title":"Отчет","priority":"high","due_at":"2026-10-24T23:59:59+05:00"}`,
	} {
		call := chatAgentCall("create_kanban_task", args)
		got := chatBindDirectoryParty(call, []aiReadEvidence{source}, "Создай задачу для t.company\nНазвание Отчет, приоритет высокий, срок 2026-10-24")
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(got.Function.Arguments), &parsed))
		require.Equal(t, "t.company", parsed["participant_query"])
		require.Equal(t, []any{float64(38)}, parsed["assignee_ids"])
		require.Equal(t, "Отчет", parsed["title"])
	}
	for _, tc := range []struct {
		name, user string
		source     aiReadEvidence
		args       string
	}{
		{"CRM contact is not an employee", "t.company", partyEvidence("crm_contacts", `{"users":[{"id":38,"username":"t.company"}]}`), `{"assignee_ids":[38]}`},
		{"different identity", "t.company", source, `{"assignee_ids":[39]}`},
		{"substring is not the login", "t.company2", source, `{"assignee_ids":[38]}`},
		{"email prefix is not the login", "t.company@other.test", source, `{"assignee_ids":[38]}`},
		{"unmentioned name", "Арсен", source, `{"assignee_ids":[38]}`},
		{"ambiguous display name", "Test Company", partyEvidence("colleague_search", `{"users":[{"id":38,"first_name":"Test","last_name":"Company"},{"id":39,"first_name":"Test","last_name":"Company"}]}`), `{}`},
		{"truncated lookup", "t.company", aiReadEvidence{Source: alem.DataSource{Dataset: "colleague_search"}, Result: json.RawMessage(`{"status":"ok","truncated":true,"data":{"users":[{"id":38,"username":"t.company"}]}}`)}, `{}`},
		{"forbidden lookup", "t.company", aiReadEvidence{Source: alem.DataSource{Dataset: "colleague_search"}, Result: json.RawMessage(`{"status":"forbidden","data":{"users":[{"id":38,"username":"t.company"}]}}`)}, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := chatAgentCall("create_kanban_task", tc.args)
			require.Equal(t, call, chatBindDirectoryParty(call, []aiReadEvidence{tc.source}, tc.user))
		})
	}
}

func TestMainChatTaskAssigneeSurvivesFollowupAndMissingModelReference(t *testing.T) {
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
				require.Equal(t, "t.company", r.URL.Query().Get("q"))
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
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"colleague_search","params":{"q":"t.company"}}]}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("directory", `{"dataset":"colleague_search","params":{"q":"t.company"}}`)}}), nil
				}
				args := `{"title":"Отчет","priority":"high","due_at":"2026-10-24T23:59:59+05:00","assignee_ids":[38]}`
				if jsonMode {
					return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"create_kanban_task","arguments":` + args + `}`}), nil
				}
				return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("create_kanban_task", args)}}), nil
			})
			resp, err := completeWithUserData(ctx, client, reader, identity, alem.ChatCompletionRequest{Messages: []alem.Message{
				{Role: "user", Content: "Создай задачу для t.company"},
				{Role: "assistant", Content: "Какое название задачи, приоритет и срок?"},
				{Role: "user", Content: "Название Отчет, приоритет высокий, срок 2026-10-24"},
			}}, &SmartAgentService{actionStore: store})
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Len(t, store, 1)
			require.Len(t, resp.DataCards, 1)
			require.NotNil(t, resp.DataCards[0].Confirmation)
			require.NotContains(t, resp.Choices[0].Message.Content, "уточните")
			for _, raw := range store {
				require.Contains(t, raw, `Отчет`)
				require.Contains(t, raw, `t.company`)
			}
		})
	}
}

func TestMainChatDirectoryBindingKeepsPersonalEventWithoutAttendees(t *testing.T) {
	call := chatAgentCall("create_calendar_event", `{"attendee_ids":[],"title":"Обзор"}`)
	source := partyEvidence("colleague_search", `{"users":[{"id":38,"username":"t.company"}]}`)
	require.Equal(t, call, chatBindDirectoryParty(call, []aiReadEvidence{source}, "Вчера писал t.company. Создай личную встречу Обзор без гостей"))
}

func TestMainChatMissingRecipientCanResolveInternally(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	store := chatMemoryActions{}
	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("create_kanban_task", `{"title":"Отчет","priority":"high","due_at":"2026-10-24T23:59:59+05:00"}`)}}), nil
		}
		require.Contains(t, req.Messages[len(req.Messages)-1].Content, "action_participant_resolution_required")
		return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall("create_kanban_task", `{"title":"Отчет","priority":"high","due_at":"2026-10-24T23:59:59+05:00","assignee_ids":[38],"participant_query":"t.company"}`)}}), nil
	})
	resp, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: `Создай задачу для t.company. Название Отчет, приоритет высокий, срок 2026-10-24`}}}, &SmartAgentService{actionStore: store})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Len(t, store, 1)
	require.NotNil(t, resp.DataCards[0].Confirmation)
	require.Empty(t, resp.DataSources)
}

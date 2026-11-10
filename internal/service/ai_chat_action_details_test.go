package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func chatActionFixtures() map[string]struct{ raw, text string } {
	event, task := uuid.NewString(), uuid.NewString()
	return map[string]struct{ raw, text string }{
		"create_kanban_task":    {`{"title":"Отчет","priority":"high","due_at":"2026-10-24T18:00:00+05:00","assignee_ids":[38],"participant_query":"Арсен"}`, "Создай задачу Отчет для Арсена, приоритет высокий, срок 2026-10-24"},
		"create_calendar_event": {`{"title":"Обзор","start_time":"2026-10-24T10:00:00+05:00","end_time":"2026-10-24T11:00:00+05:00","attendee_ids":[]}`, "Личная встреча Обзор 2026-10-24 с 10:00 до 11:00"},
		"create_online_meeting": {`{"meeting_name":"Обзор","from_date":"2026-10-24T10:00:00+05:00","to_date":"2026-10-24T11:00:00+05:00","attendee_ids":[38],"participant_query":"Арсен"}`, "Видеовстреча Обзор с Арсеном 2026-10-24 с 10:00 до 11:00"},
		"send_broadcast":        {`{"message":"Встречу переносим на завтра","recipient_user_ids":[38],"participant_query":"Арсен"}`, "Напиши Арсену: Встречу переносим на завтра"},
		"set_event_reminder":    {`{"event_id":"` + event + `","event_query":"Обзор","minutes_before":15}`, "Напомни о встрече Обзор за 15 минут"},
		"cancel_calendar_event": {`{"event_id":"` + event + `","event_query":"Обзор"}`, "Отмени встречу Обзор"},
		"close_kanban_task":     {`{"task_id":"` + task + `","task_query":"Отчет"}`, "Закрой задачу Отчет"},
		"prepare_email_draft":   {`{"recipient":"Арсен","subject":"План","body":"Встречу переносим на завтра"}`, "Напиши Арсену письмо. Тема: План. Текст: Встречу переносим на завтра"},
	}
}

func TestMainChatActionContractsCoverEveryWriteAndEveryRequiredField(t *testing.T) {
	schemas := map[string]alem.FunctionTool{}
	for _, tool := range mainChatToolSchemas() {
		schemas[tool.Function.Name] = tool
	}
	for name := range aiChatWriteTitles {
		_, ok := mainChatActionContracts[name]
		require.True(t, ok, "write tool %s has no admission contract", name)
	}
	for name, fixture := range chatActionFixtures() {
		t.Run(name, func(t *testing.T) {
			ctx, identity := chatTestIdentity(t)
			var complete map[string]any
			require.NoError(t, json.Unmarshal([]byte(fixture.raw), &complete))
			required, ok := schemas[name].Function.Parameters["required"].([]string)
			require.True(t, ok)
			for _, field := range mainChatActionContracts[name].fields {
				require.Contains(t, required, field.key)
				partial := map[string]any{}
				for key, value := range complete {
					if key != field.key {
						partial[key] = value
					}
				}
				encoded, _ := json.Marshal(partial)
				store := chatMemoryActions{}
				agent := &SmartAgentService{actionStore: store}
				result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall(name, string(encoded)), fixture.text)
				require.Equal(t, "clarification_required", result.Status, "missing %s", field.key)
				require.False(t, pending)
				require.Empty(t, cards)
				require.Empty(t, store)
			}
			store := chatMemoryActions{}
			agent := &SmartAgentService{actionStore: store}
			result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall(name, fixture.raw), fixture.text)
			if name == "prepare_email_draft" {
				require.Equal(t, "draft_prepared", result.Status)
				require.False(t, pending)
				require.Empty(t, store)
				require.Nil(t, cards[0].Confirmation)
			} else {
				require.Equal(t, "confirmation_required", result.Status)
				require.True(t, pending)
				require.Len(t, store, 1)
			}
		})
	}
}

func TestMainChatActionsDoNotInventContentTimesOrTargets(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	for name, fixture := range chatActionFixtures() {
		t.Run(name, func(t *testing.T) {
			store := chatMemoryActions{}
			agent := &SmartAgentService{actionStore: store}
			result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall(name, fixture.raw), "пожалуйста выполни действие")
			require.Equal(t, "clarification_required", result.Status)
			require.Empty(t, cards)
			require.False(t, pending)
			require.Empty(t, store)
		})
	}
	fixture := chatActionFixtures()["create_calendar_event"]
	agent := &SmartAgentService{actionStore: chatMemoryActions{}}
	result, _, pending := agent.runMainChatTool(ctx, identity, chatAgentCall("create_calendar_event", fixture.raw), "Личная встреча Обзор 2026-10-24 в 10:00")
	require.Equal(t, "clarification_required", result.Status)
	require.Contains(t, result.Message, "длительность")
	require.False(t, pending, "one hour may not be inferred as a default")
	require.False(t, chatActionClockInUserText("15.04.2026", time.Date(2026, 4, 15, 15, 4, 0, 0, time.FixedZone("Almaty", 5*3600))))
	require.False(t, chatActionDurationInUserText("115 минут", 15*time.Minute))
}

func TestMainChatEmailBriefMustContainContentBeyondRecipient(t *testing.T) {
	require.False(t, chatEmailBriefSupplied("Напиши письмо Арсену", "Арсен", "Напиши письмо Арсену"))
	require.False(t, chatEmailBriefSupplied("про перенос встречи", "Арсен", "Напиши письмо Арсену"))
	require.True(t, chatEmailBriefSupplied("о переносе встречи на завтра", "Арсен", "Напиши письмо Арсену о переносе встречи на завтра"))
	ctx, identity := chatTestIdentity(t)
	agent := &SmartAgentService{actionStore: chatMemoryActions{}}
	result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall("prepare_email_draft", `{"recipient":"Арсен","subject":"Перенос встречи","body":"Арсен, предлагаю перенести встречу на завтра.","content_brief":"о переносе встречи на завтра"}`), "Напиши письмо Арсену о переносе встречи на завтра")
	require.Equal(t, "draft_prepared", result.Status)
	require.False(t, pending)
	require.Nil(t, cards[0].Confirmation)
}

func TestMainChatAllActionClarificationsUseBothProtocols(t *testing.T) {
	for name := range mainChatActionContracts {
		for _, jsonMode := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/native", true: "/json"}[jsonMode], func(t *testing.T) {
				ctx, identity := chatTestIdentity(t)
				store := chatMemoryActions{}
				agent := &SmartAgentService{actionStore: store}
				calls := 0
				client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
					calls++
					if jsonMode && req.Tools != nil {
						return nil, &alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Param: "tools"}}
					}
					if jsonMode {
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"` + name + `","arguments":{}}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall(name, `{}`)}}), nil
				})
				response, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: "Подготовь действие"}}}, agent)
				require.NoError(t, err)
				require.Contains(t, response.Choices[0].Message.Content, "уточните")
				require.Empty(t, response.DataCards)
				require.Empty(t, store)
				require.LessOrEqual(t, calls, 2)
			})
		}
	}
}

func TestMainChatEmailAndMeetingCollectOnlyMissingDetailsAcrossTurns(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, name := range []string{"prepare_email_draft", "create_online_meeting"} {
			t.Run(name+map[bool]string{false: "/native", true: "/json"}[jsonMode], func(t *testing.T) {
				ctx, identity := chatTestIdentity(t)
				store := chatMemoryActions{}
				agent := &SmartAgentService{actionStore: store}
				first := "Напиши письмо Арсену"
				raw := `{"recipient":"Арсен"}`
				if name == "create_online_meeting" {
					first = "Создай митинг с Арсеном"
					raw = `{"attendee_ids":[38],"participant_query":"Арсен"}`
				}
				client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
					if jsonMode && req.Tools != nil {
						return nil, &alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Param: "tools"}}
					}
					if jsonMode {
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"` + name + `","arguments":` + raw + `}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall(name, raw)}}), nil
				})
				history := []alem.Message{{Role: "user", Content: first}}
				response, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: history}, agent)
				require.NoError(t, err)
				require.Empty(t, store)
				require.Empty(t, response.DataCards)
				require.NotContains(t, response.Choices[0].Message.Content, "кому")
				require.NotContains(t, response.Choices[0].Message.Content, "участников")
				followup := "О переносе встречи на завтра."
				raw = `{"recipient":"Арсен","subject":"Перенос встречи","body":"Предлагаю перенести встречу на завтра.","content_brief":"о переносе встречи на завтра"}`
				if name == "create_online_meeting" {
					followup = "Название Обзор, 24 октября 2026, в 10 на 30 минут"
					raw = `{"meeting_name":"Обзор","from_date":"2026-10-24T10:00:00+05:00","to_date":"2026-10-24T10:30:00+05:00","attendee_ids":[38],"participant_query":"Арсен"}`
				}
				history = append(history, response.Choices[0].Message, alem.Message{Role: "user", Content: followup})
				response, err = completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: history}, agent)
				require.NoError(t, err)
				require.Len(t, response.DataCards, 1)
				if name == "prepare_email_draft" {
					require.Empty(t, store)
					require.Nil(t, response.DataCards[0].Confirmation)
				} else {
					require.Len(t, store, 1)
					require.NotNil(t, response.DataCards[0].Confirmation)
				}
			})
		}
	}
}

func TestMainChatValidatedPreviewMatchesFrozenArguments(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	store := chatMemoryActions{}
	agent := &SmartAgentService{actionStore: store}
	fixture := chatActionFixtures()["create_calendar_event"]
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(fixture.raw), &args))
	args["description"] = "Мы гарантируем выплату премии"
	raw, _ := json.Marshal(args)
	result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall("create_calendar_event", string(raw)), fixture.text)
	require.Equal(t, "confirmation_required", result.Status)
	require.True(t, pending)
	key, err := chatActionKey(ctx, identity, cards[0].Confirmation.Token)
	require.NoError(t, err)
	var frozen aiPendingChatAction
	require.NoError(t, json.Unmarshal([]byte(store[key]), &frozen))
	require.NotContains(t, frozen.Arguments, "description")
}

func TestSmartAgentLegacyCannotExecuteIncompleteActionBatch(t *testing.T) {
	org := uuid.New()
	user := &domain.User{ID: 42, OrganizationID: &org, IsActive: true}
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	providerCalls := 0
	client := completionFunc(func(_ context.Context, _ alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "", []alem.ToolCall{chatAgentCall("create_calendar_event", `{"title":"Встреча","start_time":"2026-10-24T10:00:00+05:00","end_time":"2026-10-24T11:00:00+05:00"}`)}, "tool_calls", `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`), nil
	})
	agent := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, client)
	agent.SetDefaultModel(DefaultAlemModel)
	// No CalendarService is configured: any attempted execution would panic.
	response, err := agent.ProcessChat(context.Background(), user.ID, &SmartAgentChatRequest{Message: "Создай встречу", SessionID: uuid.NewString()})
	require.NoError(t, err)
	require.Contains(t, response.Reply, "уточните")
	require.Nil(t, response.Widget)
	require.Equal(t, 1, providerCalls)
}

func TestMainChatActionCannotInventParticipantsOrSubstituteEmail(t *testing.T) {
	ctx, identity := chatTestIdentity(t)
	for _, name := range []string{"create_kanban_task", "send_broadcast", "create_online_meeting"} {
		fixture := chatActionFixtures()[name]
		store := chatMemoryActions{}
		agent := &SmartAgentService{actionStore: store}
		text := strings.ReplaceAll(fixture.text, "Арсен", "")
		result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall(name, fixture.raw), text)
		require.Equal(t, "clarification_required", result.Status)
		require.Empty(t, cards)
		require.False(t, pending)
		require.Empty(t, store)
	}
	agent := &SmartAgentService{actionStore: chatMemoryActions{}}
	fixture := chatActionFixtures()["send_broadcast"]
	result, cards, pending := agent.runMainChatTool(ctx, identity, chatAgentCall("send_broadcast", fixture.raw), "Напиши письмо Арсену: Встречу переносим на завтра")
	require.Equal(t, "email_draft_required", result.Status)
	require.Empty(t, cards)
	require.False(t, pending)
	reminder := chatActionFixtures()["set_event_reminder"]
	result, _, pending = agent.runMainChatTool(ctx, identity, chatAgentCall("set_event_reminder", reminder.raw), "Напомни о встрече Обзор через 15 минут")
	require.Equal(t, "clarification_required", result.Status)
	require.False(t, pending)
}

func TestMainChatLegacyBatchPreflightIsAllOrNothing(t *testing.T) {
	fixtures := chatActionFixtures()
	first := chatAgentCall("send_broadcast", fixtures["send_broadcast"].raw)
	second := chatAgentCall("create_calendar_event", `{}`)
	second.ID = "second"
	prepared, err := preflightChatActionBatch([]alem.ToolCall{first, second}, llmToolsToAlem(getToolsSchema()), fixtures["send_broadcast"].text)
	require.Error(t, err)
	require.Nil(t, prepared)
	second = first
	_, err = preflightChatActionBatch([]alem.ToolCall{first, second}, llmToolsToAlem(getToolsSchema()), fixtures["send_broadcast"].text)
	require.Error(t, err, "duplicate tool IDs must not swap frozen action parameters")
}

func TestMainChatActionTitleIsNotJustTheCreationRequest(t *testing.T) {
	require.False(t, chatActionTitleInUserText("Встреча с Арсеном", "Создай встреча с Арсеном завтра в 10"))
	require.False(t, chatActionTitleInUserText("создай задачу для Арсена", "создай задачу для Арсена"))
	require.False(t, chatActionTitleInUserText("task for Arsen", "create task for Arsen"))
	require.True(t, chatActionTitleInUserText("Встреча с Арсеном", "Название: Встреча с Арсеном"))
	require.True(t, chatActionTitleInUserText("task for Arsen", `Create a task named "task for Arsen"`))
}

func TestMainChatEmailPreparationFeedbackIsNotAnEvidenceSource(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, compose := range []bool{false, true} {
			t.Run(map[bool]string{false: "redirect", true: "compose"}[compose]+map[bool]string{false: "/native", true: "/json"}[jsonMode], func(t *testing.T) {
				ctx, identity := chatTestIdentity(t)
				store := chatMemoryActions{}
				agent := &SmartAgentService{actionStore: store}
				steps := 0
				client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
					if jsonMode && req.Tools != nil {
						return nil, &alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Param: "tools"}}
					}
					steps++
					name, raw := "prepare_email_draft", `{"recipient":"Арсен"}`
					if compose {
						if steps == 1 {
							raw = `{"recipient":"Арсен","content_brief":"о переносе встречи на завтра"}`
						} else {
							raw = `{"recipient":"Арсен","content_brief":"о переносе встречи на завтра","subject":"Перенос встречи","body":"Предлагаю перенести встречу на завтра."}`
						}
					} else if steps == 1 {
						name = "send_broadcast"
						raw = `{"message":"Привет","recipient_user_ids":[38],"participant_query":"Арсен"}`
					}
					if jsonMode {
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"tool","name":"` + name + `","arguments":` + raw + `}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{chatAgentCall(name, raw)}}), nil
				})
				text := "Напиши письмо Арсену"
				if compose {
					text += " о переносе встречи на завтра"
				}
				response, err := completeWithUserData(ctx, client, &fakePersonalReader{}, identity, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: text}}}, agent)
				require.NoError(t, err)
				require.Equal(t, 2, steps)
				require.Empty(t, response.DataSources)
				require.Empty(t, store)
				if compose {
					require.Len(t, response.DataCards, 1)
					require.Nil(t, response.DataCards[0].Confirmation)
				} else {
					require.Empty(t, response.DataCards)
					require.Contains(t, response.Choices[0].Message.Content, "что написать")
				}
			})
		}
	}
}

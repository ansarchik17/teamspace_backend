package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestPersonalAIGeneralConversationNeedsNoDataRead(t *testing.T) {
	cases := []struct{ question, answer string }{
		{"что ты умеешь", "Могу объяснять темы, писать и редактировать тексты, переводить, помогать с кодом и анализировать доступные вам рабочие данные."},
		{"Привет", "Привет! Чем помочь?"},
		{"Объясни фотосинтез", "Растения используют свет, воду и углекислый газ для образования органических веществ."},
		{"Сколько будет 17 * 23?", "17 × 23 = 391."},
		{"Напиши письмо клиенту", "Здравствуйте! Направляю вам предложение о сотрудничестве."},
		{"Translate: Доброе утро", "Good morning."},
		{"HTTP 403: что значит? Покажи JSON и Python", "HTTP 403 означает запрет доступа.\n\n```json\n{\"status\": 403, \"items\": []}\n```\n\n```python\npage = 1\nprint(page)\n```\nПример метки массива: [S1]."},
		{"Қазақша жауап бер: сен не істей аласың?", "Мәтін жазуға, аударуға және сұрақтарға жауап беруге көмектесемін."},
		{"Сократи это до двух предложений", "Первое предложение. Второе предложение."},
		{"Помоги разобраться", "Что именно нужно разобрать?"},
	}
	for _, protocol := range []string{"native", "ignored-tools", "rejected-tools"} {
		for _, tc := range cases {
			t.Run(protocol+"/"+tc.question, func(t *testing.T) {
				reader := &fakePersonalReader{}
				calls := 0
				history := []alem.Message{{Role: "user", Content: "Текст для редактирования"}, {Role: "assistant", Content: "Предыдущий ответ"}, {Role: "user", Content: tc.question}}
				client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
					calls++
					if len(reader.queries) != 1 || reader.queries[0].Dataset != "profile" {
						t.Fatal("general conversation read business data")
					}
					if len(req.Messages) != 4 || req.Messages[1].Content != history[0].Content || req.Messages[2].Content != history[1].Content || req.Messages[3].Content != tc.question {
						t.Fatal("conversation history lost during routing")
					}
					if calls == 1 {
						if len(req.Tools) != 1 || req.ToolChoice != "auto" {
							t.Fatal("first completion must choose whether it needs data")
						}
						if protocol == "ignored-tools" {
							return personalResponse(alem.Message{Role: "assistant", Content: "discard this unclassified answer"}), nil
						}
						if protocol == "rejected-tools" {
							return nil, &alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Param: "tools"}}
						}
					} else if len(req.Tools) > 0 || req.ToolChoice != "" || !strings.Contains(req.Messages[0].Content.(string), aiJSONReadProtocol) {
						t.Fatal("incompatible fallback protocol")
					}
					envelope := map[string]any{"answer_mode": "general", "answer": tc.answer, "card_ids": []string{}}
					if protocol != "native" {
						envelope["action"] = "answer"
					}
					wire, _ := json.Marshal(envelope)
					return personalResponse(alem.Message{Role: "assistant", Content: string(wire)}), nil
				})
				resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Messages: history})
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := 1
				if protocol != "native" {
					wantCalls = 2
				}
				wantTokens := 5 * wantCalls
				if protocol == "rejected-tools" {
					wantTokens = 5
				}
				if calls != wantCalls || resp.Usage.TotalTokens != wantTokens || resp.Choices[0].Message.Content != tc.answer || len(resp.DataSources) != 0 || len(resp.DataCards) != 0 {
					t.Fatalf("general answer was altered or given fake evidence: %+v", resp)
				}
			})
		}
	}
}

func TestPersonalAIGroundedAnswerStillRequiresRead(t *testing.T) {
	for _, envelope := range []string{
		`{"answer_mode":"grounded","answer":"У вас три задачи.","card_ids":[]}`,
		`{"answer_mode":"general","answer":"У вас три задачи.","card_ids":["invented"]}`,
		`{"answer_mode":"unknown","answer":"У вас три задачи."}`,
		`{"answer":"У вас три задачи."}`,
		`{"answer_mode":"general","answer":""}`,
		`{"answer_mode":"general","answer":"Ответ"} {"answer":"Лишний объект"}`,
	} {
		t.Run(envelope, func(t *testing.T) {
			reader := &fakePersonalReader{}
			calls := 0
			client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				calls++
				if calls == 1 {
					return personalResponse(alem.Message{Role: "assistant", Content: envelope}), nil
				}
				return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer_mode":"grounded","answer":"У вас три задачи."}`}), nil
			})
			if _, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: "Мои задачи"}}}); err == nil || calls != 2 || len(reader.queries) != 1 {
				t.Fatal("unread grounded answer accepted")
			}
		})
	}
}

func TestPersonalAIReadAfterGeneralTurnAndFollowUp(t *testing.T) {
	reader := &fakePersonalReader{}
	calls := 0
	history := []alem.Message{{Role: "user", Content: "что ты умеешь"}, {Role: "assistant", Content: "Помогаю с вопросами и рабочими данными."}, {Role: "user", Content: "Покажи мои задачи на сегодня"}, {Role: "assistant", Content: "Сегодня задач нет."}, {Role: "user", Content: "А на завтра?"}}
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			if req.Messages[len(req.Messages)-1].Content != "А на завтра?" || req.Messages[3].Content != history[2].Content {
				t.Fatal("follow-up lost its task context")
			}
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("tasks", `{"dataset":"tasks","params":{"mine":"true"}}`)}}), nil
		}
		if req.Messages[len(req.Messages)-1].Role != "tool" {
			t.Fatal("missing verified follow-up results")
		}
		return personalResponse(alem.Message{Role: "assistant", Content: `{"answer_mode":"grounded","answer":"Задача Review.","card_ids":[]}`}), nil
	})
	resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Messages: history})
	if err != nil || calls != 2 || len(reader.queries) != 2 || len(resp.DataSources) != 1 || resp.Choices[0].Message.Content != "Задача Review." {
		t.Fatalf("general history prevented grounded follow-up: %v %+v", err, resp)
	}
}

func TestAIAnswerModeRejectsMixedReadAndGeneralCards(t *testing.T) {
	for _, text := range []string{
		`{"action":"read","answer_mode":"general","queries":[{"dataset":"tasks"}]}`,
		`{"action":"answer","answer_mode":"general","answer":"Ответ","card_ids":["S1-C1"]}`,
		`{"action":"answer","answer_mode":"invented","answer":"Ответ"}`,
	} {
		if _, err := decodeAIJSONReadStep(alem.Message{Role: "assistant", Content: text}, 0); err == nil {
			t.Fatalf("accepted ambiguous answer envelope: %s", text)
		}
	}
	for _, role := range []string{"user", "tool", "system"} {
		msg := decodeAIDataAnswer(alem.Message{Role: role, Content: `{"answer_mode":"general","answer":"Ответ"}`})
		if msg.AnswerMode != "" {
			t.Fatal(fmt.Sprintf("accepted routing decision from %s", role))
		}
	}
}

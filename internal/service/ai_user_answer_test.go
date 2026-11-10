package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/alem"
)

type requiredContextReader struct {
	queries       []AIDataQuery
	contextStatus string
}

func (r *requiredContextReader) Catalog() []AIDataDataset {
	return []AIDataDataset{
		{Key: "profile"},
		{Key: "odo_assignments", Title: "Мои поручения", RequiredContext: []string{"odo_sla"}, CardViews: []AIDataCardView{{ItemsPaths: []string{"assignments"}, Service: "ЭДО", Title: []string{"title"}}}},
		{Key: "odo_sla", Title: "Мои документы ЭДО", CardViews: []AIDataCardView{{ItemsPaths: []string{"documents"}, Service: "ЭДО", Title: []string{"title"}, Status: []string{"status"}, LinkID: []string{"id"}, Link: "/web/eps?document="}}},
	}
}
func (r *requiredContextReader) Read(_ context.Context, identity AIDataIdentity, query AIDataQuery) AIDataResult {
	if identity.UserID != 42 {
		panic("identity changed")
	}
	r.queries = append(r.queries, query)
	switch query.Dataset {
	case "profile":
		return AIDataResult{Status: "ok", Data: map[string]any{"id": 42}}
	case "odo_assignments":
		return AIDataResult{Status: "ok", Data: map[string]any{"assignments": []any{}}}
	case "odo_sla":
		if r.contextStatus != "" {
			return AIDataResult{Status: r.contextStatus}
		}
		return AIDataResult{Status: "ok", Data: map[string]any{"current_user_id": "odo-user", "documents": []any{map[string]any{"id": "doc-1", "title": "Договор на поставку", "status": "waiting_for_signature", "signer_id": "odo-user", "available_actions": []any{"sign"}}}}}
	}
	return AIDataResult{Status: "invalid_request"}
}

func TestPersonalAIReadsRequiredContextAndPublishesHumanAnswer(t *testing.T) {
	for _, jsonProtocol := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "json"}[jsonProtocol], func(t *testing.T) {
			reader := &requiredContextReader{}
			publicationCalls := 0
			client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				if strings.HasPrefix(req.Messages[0].Content.(string), "Ты готовишь ответ") {
					publicationCalls++
					payload := req.Messages[1].Content.(string)
					if len(reader.queries) != 3 || !strings.Contains(payload, "Договор на поставку") || !strings.Contains(payload, "signer_id") {
						t.Fatalf("review received incomplete evidence: %s", payload)
					}
					return personalResponse(alem.Message{Role: "assistant", Content: `{"answer":"Вам нужно подписать договор.","card_ids":["S2-C1"]}`}), nil
				}
				if jsonProtocol {
					for _, message := range req.Messages {
						if message.Role == "tool" || len(message.ToolCalls) > 0 {
							t.Fatal("native tools leaked into JSON gateway")
						}
					}
				}
				if len(reader.queries) == 1 {
					if jsonProtocol && len(req.Tools) > 0 {
						return personalResponse(alem.Message{Role: "assistant", Content: "unsupported native tools"}), nil
					}
					if jsonProtocol {
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"odo_assignments"}]}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("initial", `{"dataset":"odo_assignments"}`)}}), nil
				}
				// The model makes the screenshot's exact kind of error. The server
				// must read the other action context before allowing completion.
				bad := "Согласно odo_assignments, список «assignments» пуст, status=todo. В ЭДО нет документов, требующих действия. 【S1】"
				if jsonProtocol {
					wire, _ := json.Marshal(map[string]any{"action": "answer", "answer": bad})
					return personalResponse(alem.Message{Role: "assistant", Content: string(wire)}), nil
				}
				for index, message := range req.Messages {
					if message.Role == "tool" {
						found := false
						for previous := index - 1; previous >= 0; previous-- {
							for _, call := range req.Messages[previous].ToolCalls {
								found = found || call.ID == message.ToolCallID
							}
						}
						if !found {
							t.Fatalf("invalid native context call: %+v", message)
						}
					}
				}
				return personalResponse(alem.Message{Role: "assistant", Content: bad}), nil
			})
			resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Messages: []alem.Message{{Role: "user", Content: "Какие документы в ЭДО требуют моего действия?"}}})
			if err != nil {
				t.Fatal(err)
			}
			if publicationCalls != 1 || len(reader.queries) != 3 || reader.queries[2].Dataset != "odo_sla" {
				t.Fatalf("required context not read: %+v", reader.queries)
			}
			if resp.Choices[0].Message.Content != "Вам нужно подписать договор." || len(resp.DataCards) != 1 || resp.DataCards[0].Title != "Договор на поставку" {
				t.Fatalf("technical/unverified draft returned: %+v", resp)
			}
		})
	}
}

func TestPersonalAIRequiredContextFailureIsNotRetriedOrTreatedAsEmpty(t *testing.T) {
	reader := &requiredContextReader{contextStatus: "unavailable"}
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		if strings.HasPrefix(req.Messages[0].Content.(string), "Ты готовишь ответ") {
			if !strings.Contains(req.Messages[1].Content.(string), `"context_incomplete":true`) {
				t.Fatal("missing partial verification flag")
			}
			return nil, errors.New("provider broke")
		}
		if len(reader.queries) == 1 {
			return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("initial", `{"dataset":"odo_assignments"}`)}}), nil
		}
		return personalResponse(alem.Message{Role: "assistant", Content: "odo_assignments: нет документов в ЭДО"}), nil
	})
	resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.queries) != 3 || len(resp.DataCards) != 1 || !resp.DataCards[0].Empty || resp.DataSources[1].Status != "unavailable" {
		t.Fatalf("failed dependency retried or misrepresented: %+v", resp)
	}
	if resp.Choices[0].Message.Content != "В проверенных списках записей не найдено. Часть данных пока не удалось проверить." {
		t.Fatal("broad no-documents claim escaped")
	}
}

func TestPersonalAIPublisherRejectsTechnicalOutputAndInventedCards(t *testing.T) {
	for _, bad := range []string{`{"answer":"odo_sla invalid_request","card_ids":["S1-C1"]}`, `{"answer":"Вы должны подписать неизвестный документ.","card_ids":["invented"]}`} {
		calls := 0
		client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
			calls++
			return personalResponse(alem.Message{Role: "assistant", Content: bad}), nil
		})
		candidates := map[string]alem.DataCard{"S1-C1": {ID: "S1-C1", SourceID: "S1", Title: "Договор", Service: "ЭДО"}}
		_, _, err := publishAIUserAnswer(context.Background(), client, "test", "Мои документы", alem.Message{Content: "odo_sla: нет документов"}, []aiReadEvidence{{Source: alem.DataSource{ID: "S1"}}}, candidates, (&requiredContextReader{}).Catalog(), false)
		if err == nil || calls != 2 {
			t.Fatal("unvalidated publisher output reached user")
		}
	}
}

func TestAIUserAnswerCitationsAndOrdinaryWords(t *testing.T) {
	if got := cleanAIUserAnswer("Ответ [S1] 【S2】 【S3, S4】."); got != "Ответ." {
		t.Fatalf("raw citations leaked: %s", got)
	}
	if hasAIInternalText("You have no assignments in your inbox.", (&requiredContextReader{}).Catalog()) {
		t.Fatal("ordinary language incorrectly blocked")
	}
	if !hasAIInternalText("odo\\_assignments", (&requiredContextReader{}).Catalog()) {
		t.Fatal("escaped internal name escaped validation")
	}
}

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/alem"
)

type cardReader struct{ queries []AIDataQuery }

func (r *cardReader) Catalog() []AIDataDataset {
	return []AIDataDataset{{Key: "profile"}, {Key: "odo_sla", Title: "Мои документы ЭДО", CardViews: []AIDataCardView{{ItemsPaths: []string{"documents"}, Service: "ЭДО", Title: []string{"title"}, Status: []string{"status"}, Due: []string{"due_date"}, LinkID: []string{"id"}, Link: "/web/eps?document="}}}}
}
func (r *cardReader) Read(_ context.Context, _ AIDataIdentity, query AIDataQuery) AIDataResult {
	r.queries = append(r.queries, query)
	if query.Dataset == "profile" {
		return AIDataResult{Status: "ok", Data: map[string]any{"id": 42}}
	}
	return AIDataResult{Status: "ok", Data: map[string]any{"documents": []any{map[string]any{"id": "doc / 1", "title": "Договор №42", "status": "on_signing", "due_date": "2026-10-09", "url": "https://evil.test", "secret": "secret"}}}}
}

func TestAIDataCardsUseAuthorizedValuesAndSelectedReferences(t *testing.T) {
	for _, jsonProtocol := range []bool{false, true} {
		t.Run(map[bool]string{true: "json", false: "native"}[jsonProtocol], func(t *testing.T) {
			reader := &cardReader{}
			client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
				if len(reader.queries) == 1 {
					if jsonProtocol && len(req.Tools) > 0 {
						return personalResponse(alem.Message{Role: "assistant", Content: "unsupported"}), nil
					}
					if jsonProtocol {
						return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"odo_sla"}]}`}), nil
					}
					return personalResponse(alem.Message{Role: "assistant", ToolCalls: []alem.ToolCall{personalTool("read-1", `{"dataset":"odo_sla"}`)}}), nil
				}
				last, _ := json.Marshal(req.Messages[len(req.Messages)-1])
				if !strings.Contains(string(last), "presentation_cards") || strings.Contains(string(last), "secret") {
					t.Fatalf("missing safe candidates: %s", last)
				}
				prefix := ""
				if jsonProtocol {
					prefix = `"action":"answer",`
				}
				return personalResponse(alem.Message{Role: "assistant", Content: `{` + prefix + `"answer":"Один документ на подпись [S1].","card_ids":["S1-C1","invented-id","S1-C1"]}`}), nil
			})
			resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.DataCards) != 1 {
				t.Fatalf("unverified/duplicate references accepted: %+v", resp.DataCards)
			}
			card := resp.DataCards[0]
			if card.Title != "Договор №42" || card.Href != "/web/eps?document=doc+%2F+1" || card.Status != "on_signing" || card.DueAt != "2026-10-09" || card.SourceID != "S1" {
				t.Fatalf("card values changed: %+v", card)
			}
			if strings.Contains(resp.Choices[0].Message.Content.(string), "card_ids") || strings.Contains(resp.Choices[0].Message.Content.(string), "Источники проверки:") {
				t.Fatal("technical metadata entered answer")
			}
		})
	}
}

func TestAIDataCardsDoNotTurnFailuresOrPartialDataIntoEmptyLists(t *testing.T) {
	dataset := (&cardReader{}).Catalog()[1]
	for _, result := range []AIDataResult{
		{Status: "forbidden", Data: map[string]any{"documents": []any{}}},
		{Status: "ok", Data: map[string]any{}},
		{Status: "ok", Truncated: true, Data: map[string]any{"documents": []any{}}},
		{Status: "ok", HasMoreText: true, Data: map[string]any{"documents": []any{}}},
		{Status: "ok", Data: map[string]any{"documents": []any{}, "has_next": true}},
	} {
		if cards := aiCardsFromResult(dataset, result, "S1"); len(cards) != 0 {
			t.Fatalf("invented empty state: %+v", cards)
		}
	}
	cards := aiCardsFromResult(dataset, AIDataResult{Status: "ok", Data: map[string]any{"documents": []any{}}}, "S1")
	if len(cards) != 1 || !cards[0].Empty || cards[0].Href != "" {
		t.Fatalf("missing scoped empty state: %+v", cards)
	}
}

func TestAIDataCardsIgnoreSourceURLs(t *testing.T) {
	dataset := (&cardReader{}).Catalog()[1]
	dataset.CardViews[0].Link = "https://evil.test/"
	cards := aiCardsFromResult(dataset, AIDataResult{Status: "ok", Data: map[string]any{"documents": []any{map[string]any{"id": "1", "title": "<script>alert(1)</script>", "url": "javascript:alert(1)"}}}}, "S1")
	if len(cards) != 1 || cards[0].Href != "" {
		t.Fatalf("external destination accepted: %+v", cards)
	}
}

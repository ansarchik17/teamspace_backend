package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// Presentation adapters live in the source catalogue. They do not choose
// sources, interpret the user's question, or manufacture business answers.
type AIDataCardView struct {
	EmptyTitle string             `json:"empty_title"`
	ItemsPaths []string           `json:"items_paths"`
	Service    string             `json:"service"`
	Title      []string           `json:"title_fields"`
	Status     []string           `json:"status_fields"`
	Due        []string           `json:"due_fields"`
	LinkID     []string           `json:"link_id_fields"`
	Link       string             `json:"link_prefix"`
	Fields     []AIDataCardColumn `json:"fields"`
}
type AIDataCardColumn struct {
	Label string   `json:"label"`
	Paths []string `json:"paths"`
}

func aiCardValue(data any, path string) any {
	if path == "" {
		return data
	}
	for _, key := range strings.Split(path, ".") {
		object, ok := data.(map[string]any)
		if !ok {
			return nil
		}
		data = object[key]
	}
	return data
}

func aiCardText(data any, fields []string) string {
	for _, field := range fields {
		var text string
		switch value := aiCardValue(data, field).(type) {
		case string:
			text = strings.TrimSpace(value)
		case float64:
			text = fmt.Sprint(value)
		}
		if text != "" {
			runes := []rune(text)
			if len(runes) > 240 {
				text = string(runes[:240]) + "…"
			}
			return text
		}
	}
	return ""
}

func aiCardLinkID(data any, fields []string) string {
	for _, field := range fields {
		id, ok := aiCardValue(data, field).(string)
		if ok && id != "" && len(id) <= 1024 {
			return id
		}
	}
	return ""
}

func aiCardsFromResult(dataset AIDataDataset, result AIDataResult, sourceID string) []alem.DataCard {
	if result.Status != "ok" || result.Data == nil {
		return nil
	}
	cards := []alem.DataCard{}
	for _, view := range dataset.CardViews {
		var value any
		for _, path := range view.ItemsPaths {
			candidate := aiCardValue(result.Data, path)
			switch object := candidate.(type) {
			case []any:
				value = object
			case map[string]any:
				if aiCardText(object, view.Title) != "" {
					value = object
				}
			}
			if value != nil {
				break
			}
		}
		var records []any
		switch typed := value.(type) {
		case []any:
			records = typed
		case map[string]any:
			records = []any{typed}
		default:
			continue // Missing metadata is never an empty list.
		}
		if len(records) == 0 && !result.Truncated && !result.HasMoreText && !aiDataHasMore(result.Data) {
			title := view.EmptyTitle
			if title == "" {
				title = dataset.Title
			}
			cards = append(cards, alem.DataCard{ID: fmt.Sprintf("%s-C%d", sourceID, len(cards)+1), SourceID: sourceID, Title: title, Service: view.Service, Empty: true})
		}
		for _, record := range records {
			if len(cards) >= 25 {
				break
			}
			title := aiCardText(record, view.Title)
			if title == "" {
				continue
			}
			card := alem.DataCard{ID: fmt.Sprintf("%s-C%d", sourceID, len(cards)+1), SourceID: sourceID, Title: title, Service: view.Service, Status: aiCardText(record, view.Status), DueAt: aiCardText(record, view.Due)}
			// Only catalogue-defined same-origin destinations; source/model URLs
			// are deliberately ignored. The destination rechecks object access.
			if id := aiCardLinkID(record, view.LinkID); id != "" && strings.HasPrefix(view.Link, "/web/") && !strings.ContainsAny(view.Link, "\\\r\n") {
				card.Href = view.Link + url.QueryEscape(id)
			}
			for _, field := range view.Fields {
				if text := aiCardText(record, field.Paths); text != "" {
					card.Fields = append(card.Fields, alem.DataCardField{Label: field.Label, Value: text})
				}
			}
			cards = append(cards, card)
		}
	}
	return cards
}

func selectAIDataCards(ids []string, candidates map[string]alem.DataCard) []alem.DataCard {
	cards := []alem.DataCard{}
	seen := map[string]bool{}
	for _, id := range ids {
		card, ok := candidates[id]
		if !ok || len(cards) >= 12 {
			continue
		}
		key := card.Service + "|" + card.Href + "|" + card.Title + "|" + card.Status + "|" + card.DueAt
		if seen[key] {
			continue
		}
		seen[key] = true
		cards = append(cards, card)
	}
	return cards
}

// Native tool providers return this final presentation envelope. Plain text
// remains compatible with older providers; neither format can invent a card.
func decodeAIDataAnswer(message alem.Message) alem.Message {
	text, ok := message.Content.(string)
	if !ok || message.Role != "assistant" || len(text) > 72000 || len(message.ToolCalls) > 0 {
		return message
	}
	text = aiJSONEnvelopeContent(text)
	var answer struct {
		Action     string   `json:"action"`
		Answer     string   `json:"answer"`
		CardIDs    []string `json:"card_ids"`
		AnswerMode string   `json:"answer_mode"`
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&answer) == nil && decoder.Decode(new(any)) == io.EOF && (answer.Action == "" || answer.Action == "answer") && strings.TrimSpace(answer.Answer) != "" && validAIAnswerMode(answer.AnswerMode, answer.CardIDs) {
		message.Content = answer.Answer
		message.CardIDs = answer.CardIDs
		message.AnswerMode = answer.AnswerMode
	}
	return message
}

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestAIConversationHistoryKeepsRecentCompleteTurns(t *testing.T) {
	history := []alem.Message{
		{Role: "user", Content: strings.Repeat("д", 22000)},
		{Role: "assistant", Content: strings.Repeat("a", 40000)},
		{Role: "user", Content: "Напиши письмо на казахском"},
		{Role: "assistant", Content: "Құрметті әріптестер!"},
		{Role: "user", Content: "Сделай его короче"},
	}
	bounded := limitAIConversationHistory(history)
	if len(bounded) != 3 || bounded[0].Content != history[2].Content || bounded[2].Content != history[4].Content {
		t.Fatalf("lost a recent condition or kept an orphaned reply: %+v", bounded)
	}
	encoded, _ := json.Marshal(bounded)
	if len(encoded) > 64000 {
		t.Fatal("history exceeds its budget")
	}
	if len(history) != 5 || history[1].Role != "assistant" {
		t.Fatal("source history was mutated")
	}
}

func TestAIConversationHistoryPreservesCurrentLongRequestAndVision(t *testing.T) {
	for _, content := range []any{strings.Repeat("условие задачи\n", 10000), []alem.ContentPart{{Type: "text", Text: "Что на изображении?"}, {Type: "image_url", ImageURL: &alem.ImageURL{URL: "https://example.test/image.png"}}}} {
		history := []alem.Message{{Role: "assistant", Content: "Previous reply"}, {Role: "user", Content: content}}
		bounded := limitAIConversationHistory(history)
		got, _ := json.Marshal(bounded)
		want, _ := json.Marshal(history[1:])
		if string(got) != string(want) {
			t.Fatal("current content was altered")
		}
	}
	if len(limitAIConversationHistory(nil)) != 0 {
		t.Fatal("empty history changed")
	}
}

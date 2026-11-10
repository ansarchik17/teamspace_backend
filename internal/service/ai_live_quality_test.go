package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// Opt-in provider evaluation with synthetic data. Ordinary tests never spend
// tokens or contact production services. Logged answers need human quality
// review: route checks alone do not prove correctness on arbitrary questions.
func TestPersonalAILiveQuality(t *testing.T) {
	if os.Getenv("TEAM_AI_LIVE_EVAL") != "1" {
		t.Skip("set TEAM_AI_LIVE_EVAL=1 and ALEM_API_KEY to evaluate the real provider")
	}
	key := strings.TrimSpace(os.Getenv("ALEM_API_KEY"))
	if key == "" {
		t.Fatal("ALEM_API_KEY is required for live evaluation")
	}
	model := strings.TrimSpace(os.Getenv("ALEM_DEFAULT_MODEL"))
	if model == "" {
		model = DefaultAlemModel
	}
	client := alem.NewClient(key, 60*time.Second, 0)
	cases := []struct {
		name, question string
		history        []alem.Message
		wantDataset    string
		mustContain    string
	}{
		{name: "capabilities", question: "что ты умеешь"},
		{name: "greeting", question: "Привет!"},
		{name: "explanation", question: "Объясни простыми словами, почему меняются времена года"},
		{name: "arithmetic", question: "Сколько будет 17 * 23?", mustContain: "391"},
		{name: "code", question: "Напиши Python-функцию, проверяющую, является ли число простым", mustContain: "def "},
		{name: "technical_json", question: "Что означает HTTP 403? Покажи пример JSON-ответа сервера", mustContain: "403"},
		{name: "translation", question: "Переведи на английский только фразу: Доброе утро", mustContain: "Good morning"},
		{name: "kazakh", question: "Қазақша жауап бер: сен не істей аласың?"},
		{name: "writing", question: "Напиши короткое письмо клиенту с просьбой согласовать дату встречи"},
		{name: "follow_up", question: "Сделай его короче", history: []alem.Message{{Role: "user", Content: "Напиши письмо клиенту о переносе встречи на пятницу"}, {Role: "assistant", Content: "Здравствуйте! Предлагаю перенести нашу встречу на пятницу. Подскажите, пожалуйста, будет ли вам удобно встретиться в этот день?"}}},
		{name: "personal_tasks", question: "Покажи мои задачи", wantDataset: "tasks", mustContain: "Review"},
		{name: "unavailable_mail", question: "Проверь мои последние письма", wantDataset: "mail"},
		{name: "mixed", question: "Посмотри мои задачи и предложи план работы", wantDataset: "tasks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakePersonalReader{}
			messages := append(append([]alem.Message(nil), tc.history...), alem.Message{Role: "user", Content: tc.question})
			resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Model: model, Messages: messages})
			if err != nil {
				t.Fatalf("provider evaluation failed: %v", err)
			}
			answer := resp.Choices[0].Message.Content.(string)
			t.Logf("model=%s; reads=%d; question=%s\nanswer=%s", model, len(resp.DataSources), tc.question, answer)
			if tc.wantDataset == "" {
				if len(reader.queries) != 1 || len(resp.DataSources) != 0 || len(resp.DataCards) != 0 {
					t.Error("general question triggered unnecessary system reads")
				}
			} else {
				found := false
				for _, source := range resp.DataSources {
					found = found || source.Dataset == tc.wantDataset
				}
				if !found {
					t.Errorf("personal answer did not read %s", tc.wantDataset)
				}
			}
			if tc.mustContain != "" && !strings.Contains(strings.ToLower(answer), strings.ToLower(tc.mustContain)) {
				t.Errorf("answer missing expected result %q", tc.mustContain)
			}
		})
	}
}

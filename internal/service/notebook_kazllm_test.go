package service

import (
	"context"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const notebookTestKazModel = "astanahub/alemllm"

func TestNotebookKazakhDetection(t *testing.T) {
	ru := strings.Repeat("Бюджет проекта составляет сто тысяч тенге. ", 20)
	kz := strings.Repeat("Жобаның бюджеті жүз мың теңге құрайды. ", 20)
	for _, tc := range []struct {
		question, lang string
		texts          []string
		want           bool
	}{
		{"Жобаның бюджеті қанша?", "", []string{ru}, true},
		{"Какой бюджет?", "kz", []string{ru}, true},
		{"Какой бюджет?", "", []string{kz}, true},
		{"Какой бюджет?", "ru", []string{ru}, false},
		{"Какой бюджет?", "", []string{"Ә"}, false},
	} {
		if got := notebookIsKazakh(tc.question, tc.lang, tc.texts); got != tc.want {
			t.Fatalf("notebookIsKazakh(%q, %q) = %v", tc.question, tc.lang, got)
		}
	}
}

func TestNotebookKazLLMAnswersKazakhFastQuestions(t *testing.T) {
	all := []string{DefaultAlemModel, notebookTestKazModel, "moonshotai/Kimi-K2-Thinking"}
	for _, tc := range []struct {
		name, question, mode string
		models               []string
		want                 string
	}{
		{"kazakh fast", "Жобаның бюджеті қанша?", "", all, notebookTestKazModel},
		{"russian fast", "Какой бюджет проекта?", "", all, DefaultAlemModel},
		{"kazllm unavailable", "Жобаның бюджеті қанша?", "", []string{DefaultAlemModel}, DefaultAlemModel},
		{"kazakh thinking", "Жобаның бюджеті қанша?", "thinking", all, "moonshotai/Kimi-K2-Thinking"},
	} {
		s, chat, ids, llm := notebookTestService("Бюджет 31000")
		s.kazModel = notebookTestKazModel
		llm.models = tc.models
		r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: tc.question, FileIDs: ids, Mode: tc.mode})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if llm.requests[0].Model != tc.want || r.Model != tc.want || r.Fallback {
			t.Fatalf("%s: model %q, response %q", tc.name, llm.requests[0].Model, r.Model)
		}
		if !strings.Contains(llm.requests[0].Messages[0].Content.(string), "на языке вопроса пользователя") {
			t.Fatalf("%s: language instruction missing", tc.name)
		}
	}
}

func TestNotebookKazLLMSkipsLargePrompts(t *testing.T) {
	s, chat, ids, llm := notebookTestService(strings.Repeat("Жобаның бюджеті жүз мың теңге құрайды.\n", 1500))
	s.kazModel = notebookTestKazModel
	llm.models = []string{DefaultAlemModel, notebookTestKazModel}
	if _, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Жобаның бюджеті қанша?", FileIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if llm.requests[0].Model != DefaultAlemModel {
		t.Fatalf("large prompt sent to KazLLM: %s", llm.requests[0].Model)
	}
}

type notebookKazFailLLM struct {
	notebookTestLLM
	err error
}

func (m *notebookKazFailLLM) CreateChatCompletion(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	if req.Model == notebookTestKazModel {
		m.requests = append(m.requests, req)
		if m.err != nil {
			return nil, m.err
		}
		return &alem.ChatCompletionResponse{Model: req.Model, AccountingUsage: []byte(`{"prompt_tokens":100,"completion_tokens":0,"total_tokens":100}`),
			Choices: []alem.Choice{{Message: alem.Message{Role: "assistant", Content: ""}}}}, nil
	}
	return m.notebookTestLLM.CreateChatCompletion(ctx, req)
}

func TestNotebookKazLLMFailureFallsBackToDefaultModel(t *testing.T) {
	for _, failure := range []error{
		&alem.APIError{StatusCode: 400, Details: alem.ErrorDetails{Message: "maximum context length exceeded"}},
		nil, // empty answer
	} {
		s, chat, ids, _ := notebookTestService("Бюджет 31000")
		s.kazModel = notebookTestKazModel
		llm := &notebookKazFailLLM{notebookTestLLM: notebookTestLLM{models: []string{DefaultAlemModel, notebookTestKazModel}}, err: failure}
		s.llm = llm
		r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Жобаның бюджеті қанша?", FileIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		if len(llm.requests) != 2 || llm.requests[1].Model != DefaultAlemModel || r.Model != DefaultAlemModel || !r.Fallback {
			t.Fatalf("no fallback after %v: %+v", failure, r)
		}
	}
	if notebookKazRetryable(domain.ErrAIReservationConflict, "") || notebookKazRetryable(&domain.AIReservationDenied{Reason: "COMPANY_WEEKLY_EXHAUSTED"}, "") {
		t.Fatal("accounting errors must not be retried on another model")
	}
}

func TestNotebookEmptyKazModelDisablesKazLLM(t *testing.T) {
	// ALEM_KAZ_MODEL= (empty) reaches the service as an empty model name.
	s, chat, ids, llm := notebookTestService("Бюджет 31000")
	s.kazModel = ""
	llm.models = []string{DefaultAlemModel, notebookTestKazModel}
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Жобаның бюджеті қанша?", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if llm.requests[0].Model != DefaultAlemModel || r.Model != DefaultAlemModel {
		t.Fatalf("disabled KazLLM used: request %q, response %q", llm.requests[0].Model, r.Model)
	}
}

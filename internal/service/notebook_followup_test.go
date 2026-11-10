package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type notebookFollowupTestReader struct {
	notebookTestReader
	messages []*domain.NotebookMessage
}

func (n notebookFollowupTestReader) GetChatHistory(ctx context.Context, user int64, chat uuid.UUID) ([]*domain.NotebookMessage, error) {
	if _, err := n.GetChat(ctx, user, chat); err != nil {
		return nil, err
	}
	return n.messages, nil
}

func TestNotebookFollowupRejectsChangedOrUnselectedSources(t *testing.T) {
	chat := uuid.New()
	sources := []NotebookResearchSource{{ID: "S1", FileID: uuid.New(), FileName: "plan.txt", Revision: strings.Repeat("a", 64), Excerpts: []NotebookSourceExcerpt{{StartLine: 1, EndLine: 5}}}}
	draft := linkNotebookCitations("Расходы 740000 [S1:L3]", chat, sources)
	if got := notebookFollowupDraft(draft, chat, sources); got != draft {
		t.Fatal("current verified draft was lost")
	}
	for _, invalid := range []string{
		"Расходы 740000",
		strings.ReplaceAll(draft, sources[0].Revision, strings.Repeat("b", 64)),
		strings.ReplaceAll(draft, "start_line=3", "start_line=0"),
		strings.ReplaceAll(draft, "end_line=3", "end_line=9"),
		strings.ReplaceAll(draft, chat.String(), uuid.NewString()),
		strings.ReplaceAll(draft, sources[0].FileID.String(), uuid.NewString()),
		strings.Repeat("Я", 16001) + draft,
	} {
		if got := notebookFollowupDraft(invalid, chat, sources); got != "" {
			t.Fatalf("unverified draft reused: %q", got)
		}
	}
	if got := notebookFollowupDraft(draft, chat, append(sources, NotebookResearchSource{FileID: uuid.New()})); got != "" {
		t.Fatal("changed selection reused a draft")
	}
}

func TestNotebookFollowupIncludesOnlyLatestVerifiedAnswerForEditing(t *testing.T) {
	s, chat, ids, llm := notebookTestService("Бюджет 920000\nРасходы 740000\nРезерв 180000")
	sources, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	draft := linkNotebookCitations("Слайд 1: бюджет920000,расходы740000,резерв180000 [S1:L1-L3]", chat, sources)
	reader := notebookFollowupTestReader{notebookTestReader: s.notebooks.(notebookTestReader), messages: []*domain.NotebookMessage{{ChatID: chat, Role: "assistant", Content: draft}}}
	s.notebooks = reader
	if _, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Сократи предыдущую презентацию", FileIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.requests[0].Messages[1].Content.(string), "<previous_answer>\n"+draft) {
		t.Fatal("verified previous presentation absent from refinement prompt")
	}
	if _, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Каков бюджет?", FileIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(llm.requests[1].Messages[1].Content.(string), "<previous_answer>") {
		t.Fatal("standalone factual question received stale draft context")
	}
	reader.messages = append(reader.messages, &domain.NotebookMessage{ChatID: chat, Role: "assistant", Content: "Последний ответ без проверенных ссылок"})
	s.notebooks = reader
	if draft := s.followupDraft(context.Background(), 7, chat, "Сократи предыдущий ответ", sources); draft != "" {
		t.Fatal("older answer reused instead of the latest answer")
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Parallel map calls need a concurrency-safe model and accounting store.
type notebookDeepLLM struct {
	mu       sync.Mutex
	maps     []string
	finals   []alem.ChatCompletionRequest
	mapReply func(prompt string) (string, error)
	final    func(prompt string) string
}

func (m *notebookDeepLLM) ListModels(context.Context) (*alem.ModelListResponse, error) {
	return &alem.ModelListResponse{}, nil
}

func (m *notebookDeepLLM) CreateChatCompletion(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	prompt := fmt.Sprint(req.Messages[len(req.Messages)-1].Content)
	var content string
	if fmt.Sprint(req.Messages[0].Content) == notebookMapSystem {
		m.mu.Lock()
		m.maps = append(m.maps, prompt)
		m.mu.Unlock()
		reply, err := m.mapReply(prompt)
		if err != nil {
			return nil, err
		}
		content = reply
	} else {
		m.mu.Lock()
		m.finals = append(m.finals, req)
		m.mu.Unlock()
		content = m.final(prompt)
	}
	return &alem.ChatCompletionResponse{Model: req.Model, AccountingUsage: []byte(`{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}`),
		Choices: []alem.Choice{{Message: alem.Message{Role: "assistant", Content: content}}}}, nil
}

type notebookLockedStore struct {
	mu   sync.Mutex
	keys []string
	deny func(key string) error
}

func (s *notebookLockedStore) Reserve(_ context.Context, req domain.AIReserveRequest) (domain.AIReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, req.IdempotencyKey)
	if s.deny != nil {
		if err := s.deny(req.IdempotencyKey); err != nil {
			return domain.AIReservation{}, err
		}
	}
	return domain.AIReservation{AttemptID: uuid.New(), Created: true}, nil
}
func (s *notebookLockedStore) FindOperation(context.Context, domain.AIOperationLookup) (*domain.AIOperationView, error) {
	return nil, nil
}
func (s *notebookLockedStore) MarkSent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}
func (s *notebookLockedStore) FinishFailure(context.Context, domain.AIFailureRequest) (domain.AIFailureResult, error) {
	return domain.AIFailureResult{Applied: true}, nil
}
func (s *notebookLockedStore) SettleSuccess(_ context.Context, req domain.AISettleRequest) (domain.AISettlement, error) {
	return domain.AISettlement{AttemptID: req.AttemptID, Applied: true}, nil
}

var notebookDeepFactLine = regexp.MustCompile(`L(\d+): [^\n]*777000`)

// notebookBigDocument has ~lines*100 characters; the only budget fact sits in
// the middle, far from what a 150k-character excerpt would contain.
func notebookBigDocument(lines int) (string, int) {
	var b strings.Builder
	fact := lines/2 + 7
	for i := 1; i <= lines; i++ {
		if i == fact {
			b.WriteString("Бюджет проекта Альфа составляет 777000 тенге.")
		} else {
			fmt.Fprintf(&b, "Раздел %d: описание регламента и порядка согласования документов %s", i, strings.Repeat("ж", 30))
		}
		if i < lines {
			b.WriteString("\n")
		}
	}
	return b.String(), fact
}

func notebookDeepService(t *testing.T, texts ...string) (*NotebookResearchService, uuid.UUID, []uuid.UUID, *notebookDeepLLM, *notebookLockedStore) {
	t.Helper()
	svc, chat, ids, _ := notebookTestService(texts...)
	llm := &notebookDeepLLM{
		mapReply: func(prompt string) (string, error) {
			if m := notebookDeepFactLine.FindStringSubmatch(prompt); m != nil {
				return "- Бюджет проекта Альфа 777000 тенге [S1:L" + m[1] + "]", nil
			}
			return "НЕТ", nil
		},
	}
	llm.final = func(prompt string) string {
		line := "0"
		if m := notebookDeepFactLine.FindStringSubmatch(prompt); m != nil {
			line = m[1]
		}
		return "Бюджет проекта Альфа — 777000 тенге [S1:L" + line + "]. Выдумка [S1:L3999]\n<followups>\n- Сравни бюджеты\n- Составь чек-лист\n</followups>"
	}
	store := &notebookLockedStore{}
	orgID := uuid.New()
	svc.llm = llm
	svc.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 7, OrganizationID: &orgID, IsActive: true}})
	return svc, chat, ids, llm, store
}

func TestNotebookDeepResearchFindsMiddleFactWithVerifiedCitation(t *testing.T) {
	doc, fact := notebookBigDocument(4000)
	s, chat, ids, llm, store := notebookDeepService(t, doc)
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Какой бюджет проекта?", FileIDs: ids, OperationID: "op-1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stats == nil || r.Stats.Mode != "deep" || r.Stats.ChunksTotal < 2 || r.Stats.ChunksAnalyzed != r.Stats.ChunksTotal {
		t.Fatalf("unexpected stats: %+v", r.Stats)
	}
	if r.Status != NotebookStatusAnswered {
		t.Fatalf("status %q", r.Status)
	}
	if len(llm.maps) != r.Stats.ChunksTotal || len(llm.finals) != 1 {
		t.Fatalf("map calls %d, final calls %d", len(llm.maps), len(llm.finals))
	}
	final := fmt.Sprint(llm.finals[0].Messages[1].Content)
	if !strings.Contains(final, "Заметки анализа") || !strings.Contains(final, fmt.Sprintf("L%d: Бюджет проекта Альфа", fact)) {
		t.Fatal("final prompt lacks notes or the cited source line")
	}
	if !strings.Contains(r.Content, fmt.Sprintf("start_line=%d", fact)) || !strings.Contains(r.Content, "не подтверждена") {
		t.Fatalf("citations not verified: %s", r.Content)
	}
	if strings.Contains(r.Content, "followups") || len(r.Suggestions) != 2 || r.Suggestions[0] != "Сравни бюджеты" {
		t.Fatalf("suggestions not extracted: %q %v", r.Content, r.Suggestions)
	}
	if r.Sources[0].Complete || len(r.Sources[0].Excerpts) < 2 {
		t.Fatalf("expected head and cited excerpts: %+v", r.Sources[0].Excerpts)
	}
	mapKeys := map[string]bool{}
	for _, key := range store.keys {
		if strings.Contains(key, ":map:") {
			if !regexp.MustCompile(`^notebook:op-1:map:0:\d+:1$`).MatchString(key) || mapKeys[key] {
				t.Fatalf("bad or duplicate map accounting key %q", key)
			}
			mapKeys[key] = true
		}
	}
	if len(mapKeys) != r.Stats.ChunksTotal || store.keys[len(store.keys)-1] != "notebook:op-1:1" {
		t.Fatalf("unexpected accounting keys %v", store.keys)
	}
}

func TestNotebookDeepResearchFailedChunkGivesPartialAnswer(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	s, chat, ids, llm, _ := notebookDeepService(t, doc)
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		if strings.Contains(prompt, "Раздел 3500:") {
			return "", errors.New("provider unavailable")
		}
		return base(prompt)
	}
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != NotebookStatusPartial || r.Stats.ChunksAnalyzed >= r.Stats.ChunksTotal {
		t.Fatalf("expected partial: %q %+v", r.Status, r.Stats)
	}
	if !strings.Contains(r.Sources[0].Message, "проанализировано частей") {
		t.Fatalf("source message: %q", r.Sources[0].Message)
	}
}

func TestNotebookDeepResearchStopsOnCreditDenial(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	s, chat, ids, llm, store := notebookDeepService(t, doc)
	store.deny = func(key string) error {
		return &domain.AIReservationDenied{Reason: "COMPANY_WEEKLY_EXHAUSTED", Scope: "company"}
	}
	_, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	var denied *domain.AIReservationDenied
	if !errors.As(err, &denied) {
		t.Fatalf("expected credit denial, got %v", err)
	}
	if len(llm.finals) != 0 {
		t.Fatal("final answer generated after denial")
	}
}

func TestNotebookDeepResearchReportsNothingFound(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	s, chat, ids, llm, _ := notebookDeepService(t, doc)
	llm.mapReply = func(string) (string, error) { return "НЕТ.", nil }
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "кто директор?", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != NotebookStatusNotFound || !strings.Contains(fmt.Sprint(llm.finals[0].Messages[1].Content), "сведений по вопросу не найдено") {
		t.Fatalf("expected not_found, got %q", r.Status)
	}
}

func TestNotebookDeepResearchCapsChunksAndMarksPartial(t *testing.T) {
	doc, _ := notebookBigDocument(12000)
	s, chat, ids, llm, _ := notebookDeepService(t, doc)
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stats.ChunksTotal <= notebookMaxChunks || len(llm.maps) != notebookMaxChunks || r.Status != NotebookStatusPartial {
		t.Fatalf("cap not applied: %+v maps=%d status=%q", r.Stats, len(llm.maps), r.Status)
	}
}

type notebookCountingDownloader struct {
	inner fakeDriveSearchFileDownloader
	mu    sync.Mutex
	calls int
}

func (d *notebookCountingDownloader) DownloadFile(ctx context.Context, name string) (io.ReadCloser, string, int64, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return d.inner.DownloadFile(ctx, name)
}

func TestNotebookTextCacheAvoidsRepeatedExtraction(t *testing.T) {
	s, chat, ids, _ := notebookTestService("Бюджет 31000")
	downloader := &notebookCountingDownloader{inner: s.drive.fileSvc.(fakeDriveSearchFileDownloader)}
	s.drive.fileSvc = downloader
	s.texts = newNotebookTextCache(notebookTextCacheBytes, notebookTextCacheTTL)
	for i := 0; i < 2; i++ {
		if _, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: ids}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Source(context.Background(), 7, chat, ids[0], 1, 1); err != nil {
		t.Fatal(err)
	}
	if downloader.calls != 1 {
		t.Fatalf("expected one download, got %d", downloader.calls)
	}
	s.notebooks.(notebookTestReader).files[0].ContentHash = "changed"
	if _, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if downloader.calls != 2 {
		t.Fatal("changed file reused stale text")
	}
}

func TestNotebookPresetExpandsIntoQuestionAndFormat(t *testing.T) {
	s, chat, ids, llm := notebookTestService("Бюджет 31000")
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Preset: "cards", Count: 5, Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	user := fmt.Sprint(llm.requests[0].Messages[1].Content)
	system := fmt.Sprint(llm.requests[0].Messages[0].Content)
	if r.Format != "cards" || !strings.Contains(user, "Создай 5 карточек") || !strings.Contains(user, "Особый фокус: бюджет") || !strings.Contains(system, notebookFormats["cards"]) {
		t.Fatalf("preset not expanded: format=%s user=%q", r.Format, user)
	}
	if _, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Preset: "cards", Count: 99, FileIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(llm.requests[1].Messages[1].Content), "Создай 30 карточек") {
		t.Fatal("count not clamped to preset maximum")
	}
	for _, bad := range []NotebookResearchRequest{{Preset: "unknown", FileIDs: ids}, {Preset: "compare", FileIDs: ids}, {FileIDs: ids}} {
		if _, err := s.Ask(context.Background(), 7, chat, bad); err == nil {
			t.Fatalf("invalid request accepted: %+v", bad)
		}
	}
	ru, kz := NotebookStudioPresets("ru"), NotebookStudioPresets("kz")
	if len(ru) != len(kz) || ru[0].Title == kz[0].Title || kz[0].Prompt == "" {
		t.Fatal("presets are not localized")
	}
}

type notebookHistoryTestReader struct {
	notebookTestReader
	messages []*domain.NotebookMessage
	saved    []*domain.SaveNotebookMessageRequest
}

func (r *notebookHistoryTestReader) GetChatHistory(context.Context, int64, uuid.UUID) ([]*domain.NotebookMessage, error) {
	return r.messages, nil
}

func (r *notebookHistoryTestReader) SyncMessages(_ context.Context, _ int64, _ uuid.UUID, req *domain.SaveNotebookMessageRequest) error {
	r.saved = append(r.saved, req)
	return nil
}

func TestNotebookAskUsesConversationAndPersists(t *testing.T) {
	s, chat, ids, llm := notebookTestService("1. Закупка\n2. Доставка в срок 10 дней")
	reader := &notebookHistoryTestReader{notebookTestReader: s.notebooks.(notebookTestReader), messages: []*domain.NotebookMessage{
		{ChatID: chat, Role: "user", Content: "Какие этапы проекта?"},
		{ChatID: chat, Role: "assistant", Content: "Закупка и доставка [source-0.txt, строки 1–2](/web/drive/notebook?chat=x&source=y)"},
	}}
	s.notebooks = reader
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "А подробнее по второму пункту?", FileIDs: ids, Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	prompt := fmt.Sprint(llm.requests[0].Messages[1].Content)
	if !strings.Contains(prompt, "<conversation>") || !strings.Contains(prompt, "Пользователь: Какие этапы проекта?") || strings.Contains(prompt, "/web/drive/notebook") {
		t.Fatalf("conversation missing or links not stripped: %q", prompt)
	}
	if !r.Persisted || len(reader.saved) != 1 || reader.saved[0].UserMessage != "А подробнее по второму пункту?" || reader.saved[0].AssistantType != "text" {
		t.Fatalf("exchange not persisted: %+v", reader.saved)
	}
	if r.Status != NotebookStatusAnswered || r.Stats.Mode != "single" || len(r.Suggestions) == 0 {
		t.Fatalf("unexpected response metadata: %+v", r)
	}
}

func TestNotebookAskStatusWithoutSources(t *testing.T) {
	s, chat, ids, _ := notebookTestService("")
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет"})
	if err != nil || r.Status != NotebookStatusNoSources {
		t.Fatalf("expected no_sources: %+v %v", r, err)
	}
	r, err = s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids, Persist: true})
	if err != nil || r.Status != NotebookStatusUnreadable || r.Persisted {
		t.Fatalf("expected unreadable without persistence: %+v %v", r, err)
	}
}

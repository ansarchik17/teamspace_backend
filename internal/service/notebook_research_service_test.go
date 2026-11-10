package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type notebookTestReader struct {
	owner int64
	files []*domain.DriveFile
}

func (n notebookTestReader) GetChat(_ context.Context, user int64, id uuid.UUID) (*domain.NotebookChat, error) {
	if user != n.owner {
		return nil, errors.New("forbidden notebook")
	}
	return &domain.NotebookChat{ID: id, UserID: user}, nil
}
func (n notebookTestReader) GetChatDocuments(ctx context.Context, user int64, id uuid.UUID) ([]*domain.DriveFile, error) {
	if _, err := n.GetChat(ctx, user, id); err != nil {
		return nil, err
	}
	return n.files, nil
}

type notebookTestLLM struct {
	models    []string
	requests  []alem.ChatCompletionRequest
	failFirst bool
	empty     bool
	outputs   []string
	failAt    int
	afterCall func(int)
}

func (m *notebookTestLLM) ListModels(context.Context) (*alem.ModelListResponse, error) {
	r := &alem.ModelListResponse{}
	for _, id := range m.models {
		r.Data = append(r.Data, alem.Model{ID: id})
	}
	return r, nil
}
func (m *notebookTestLLM) CreateChatCompletion(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	m.requests = append(m.requests, req)
	if m.afterCall != nil {
		m.afterCall(len(m.requests))
	}
	if m.failAt == len(m.requests) {
		return nil, errors.New("model unavailable")
	}
	if m.failFirst && len(m.requests) == 1 {
		return nil, &alem.APIError{StatusCode: 403, Details: alem.ErrorDetails{Code: "model_access_denied", Message: "model access denied"}}
	}
	if m.empty {
		return &alem.ChatCompletionResponse{}, nil
	}
	model := req.Model
	if model == "" {
		model = DefaultAlemModel
	}
	usage := []byte(`{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}`)
	if len(m.requests) <= len(m.outputs) {
		return &alem.ChatCompletionResponse{
			Model:           model,
			AccountingUsage: usage,
			Choices:         []alem.Choice{{Message: alem.Message{Role: "assistant", Content: m.outputs[len(m.requests)-1]}}},
		}, nil
	}
	return &alem.ChatCompletionResponse{
		Model:           model,
		AccountingUsage: usage,
		Choices:         []alem.Choice{{Message: alem.Message{Role: "assistant", Content: "Факт [S1:L1-L1]"}}},
	}, nil
}

func notebookTestService(texts ...string) (*NotebookResearchService, uuid.UUID, []uuid.UUID, *notebookTestLLM) {
	chat := uuid.New()
	ids := []uuid.UUID{}
	files := []*domain.DriveFile{}
	data := map[string][]byte{}
	extracted := map[string]string{}
	for i, text := range texts {
		id := uuid.New()
		ids = append(ids, id)
		name := fmt.Sprintf("source-%d.txt", i)
		object := "drive/" + name
		files = append(files, &domain.DriveFile{ID: id, OwnerID: 7, RealName: name, ObjectName: object, Size: int64(len(text))})
		data[object] = []byte(text)
		extracted[name] = text
	}
	llm := &notebookTestLLM{}
	drive := NewDriveSearchService(config.DriveSearchConfig{}, zap.NewNop())
	drive.repo = fakeDriveSearchRepo{}
	drive.fileSvc = fakeDriveSearchFileDownloader{dataByObjectName: data}
	drive.extractor = fakeDriveSearchExtractor{textByFilename: extracted}
	orgID := uuid.New()
	svc := &NotebookResearchService{notebooks: notebookTestReader{owner: 7, files: files}, drive: drive, llm: llm, logger: zap.NewNop()}
	svc.SetPaidAccounting(
		NewAIPaidCallerService(&aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}),
		alemPayerStub{user: &domain.User{ID: 7, OrganizationID: &orgID, IsActive: true}},
	)
	return svc, chat, ids, llm
}

func TestNotebookResearchReadsAllSourcesWithoutElasticAndWithout4000CharacterCut(t *testing.T) {
	s, chat, ids, _ := notebookTestService("Начало\n"+strings.Repeat("содержимое документа\n", 500)+"КОНЕЦ-8153", "Другая версия\nКОД-7713")
	sources, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{Question: "коды", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || !sources[0].Complete || !sources[1].Complete {
		t.Fatalf("sources incomplete: %+v", sources)
	}
	if !strings.Contains(sources[0].Excerpts[0].Text, "КОНЕЦ-8153") || !strings.Contains(sources[1].Excerpts[0].Text, "КОД-7713") {
		t.Fatal("source tail or another document disappeared")
	}
}

func TestNotebookResearchDeniesForeignNotebookUnattachedAndRevokedSources(t *testing.T) {
	s, chat, ids, _ := notebookTestService("private")
	if _, err := s.Context(context.Background(), 8, chat, NotebookResearchRequest{FileIDs: ids}); err == nil {
		t.Fatal("foreign notebook allowed")
	}
	if _, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: []uuid.UUID{uuid.New()}}); err == nil {
		t.Fatal("unattached source allowed")
	}
	s.drive.repo = fakeDriveSearchRepo{accessibleIDs: []uuid.UUID{}}
	if _, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: ids}); err == nil {
		t.Fatal("revoked source allowed")
	}
}

func TestNotebookResearchEmptySelectionAndEmptyFileDoNotAskModel(t *testing.T) {
	s, chat, ids, llm := notebookTestService("")
	for _, selected := range [][]uuid.UUID{nil, ids} {
		response, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: selected})
		if err != nil || response.Content == "" {
			t.Fatalf("missing explanation: response=%+v error=%v", response, err)
		}
	}
	if len(llm.requests) != 0 {
		t.Fatal("empty documents sent to model")
	}
}

func TestNotebookResearchThinkingModelDiscoveryAndFallback(t *testing.T) {
	s, chat, ids, llm := notebookTestService("Бюджет 31000")
	llm.models = []string{"moonshotai/Kimi-K2-Thinking", DefaultAlemModel}
	llm.failFirst = true
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids, Mode: "thinking"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fallback || r.Model != DefaultAlemModel || len(llm.requests) != 3 || llm.requests[2].Model != DefaultAlemModel {
		t.Fatalf("missing model fallback: %+v", r)
	}
	if !strings.Contains(r.Content, "source=") {
		t.Fatal("citation not linked")
	}
}

func TestNotebookResearchRejectsEmptyModelAnswer(t *testing.T) {
	s, chat, ids, llm := notebookTestService("данные")
	llm.empty = true
	if _, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "вопрос", FileIDs: ids}); err == nil {
		t.Fatal("empty model result accepted")
	}
}

func TestNotebookCitationRejectsChangedTextAndLongLinesKeepContent(t *testing.T) {
	text := strings.Repeat("я", 6000) + "ФИНАЛ-8604"
	wrapped := notebookLineText(text)
	if strings.ReplaceAll(wrapped, "\n", "") != text || len(strings.Split(wrapped, "\n")) != 4 {
		t.Fatal("long source line lost content")
	}
	s, chat, ids, _ := notebookTestService(text)
	sources, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Source(context.Background(), 7, chat, ids[0], 4, 4, sources[0].Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Source(context.Background(), 7, chat, ids[0], 4, 4, strings.Repeat("0", 64)); err == nil {
		t.Fatal("changed source citation accepted")
	}
}

func TestNotebookExcerptKeepsRelevantMiddleAndTailAndMarksPartial(t *testing.T) {
	lines := []string{}
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("Раздел %d. Нейтральные подробности.", i))
	}
	lines[100] = "Контрольный код СЕРЕДИНА-5729"
	lines[199] = "ФИНАЛ-8604"
	x, complete := notebookExcerpts(strings.Join(lines, "\n"), "контрольный код середина", 600)
	if complete {
		t.Fatal("partial source claimed complete")
	}
	combined := ""
	for _, e := range x {
		combined += e.Text
	}
	if !strings.Contains(combined, "СЕРЕДИНА-5729") || !strings.Contains(combined, "ФИНАЛ-8604") {
		t.Fatalf("lost middle/tail: %s", combined)
	}
}

func TestNotebookCitationsRejectInventedLinesAndSourceIDs(t *testing.T) {
	id := uuid.New()
	sources := []NotebookResearchSource{{ID: "S1", FileID: id, FileName: "a[bad].txt", Excerpts: []NotebookSourceExcerpt{{StartLine: 10, EndLine: 20}}}}
	text := linkNotebookCitations("[S1:L10-L12] [S1:L1-L5] [S9:L1-L2]", uuid.New(), sources)
	if strings.Count(text, "source=") != 1 || strings.Count(text, "не подтверждена") != 2 {
		t.Fatalf("invalid citation handling: %s", text)
	}
}

func TestNotebookNativeExtractorsPreserveFixtureCanaries(t *testing.T) {
	svc := NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())
	cases := []struct {
		name  string
		codes []string
		tool  string
	}{
		{"DR-QA-reading.pdf", []string{"ПДФ-4628", "ПДФ-ФИНАЛ-8153"}, "pdftotext"},
		{"DR-QA-reading.docx", []string{"ДОКС-2741", "ДОКС-ФИНАЛ-6938"}, ""},
		{"DR-QA-reading.pptx", []string{"СЛАЙД-3196", "СЛАЙД-ФИНАЛ-7425"}, ""},
		{"DR-QA-calculation.xlsx", []string{"Контроль!D7: 740000", "Контроль!A9: ТАБЛИЦА-5284"}, ""},
		{"DR-QA-calculation.xlsm", []string{"Контроль!D7: 740000", "Контроль!A9: ТАБЛИЦА-5284"}, ""},
		{"DR-QA-reading.ppt", []string{"СЛАЙД-3196", "СЛАЙД-ФИНАЛ-7425"}, "soffice"},
		{"DR-QA-calculation.xls", []string{"Контроль!D7: 740000", "Контроль!A9: ТАБЛИЦА-5284"}, "soffice"},
		{"DR-QA-long.md", []string{"ДЛИННЫЙ-1842", "ДЛИННЫЙ-5729", "ДЛИННЫЙ-ФИНАЛ-8604"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tool != "" {
				if _, err := exec.LookPath(tc.tool); err != nil {
					if os.Getenv("NOTEBOOK_REQUIRE_CONVERTERS") == "1" {
						t.Fatal(err)
					}
					t.Skip("converter not installed locally")
				}
			}
			buf, err := os.ReadFile("testdata/notebook/" + tc.name)
			if err != nil {
				t.Fatal(err)
			}
			text, err := svc.ExtractTextBytes(tc.name, buf)
			if err != nil {
				t.Fatal(err)
			}
			for _, code := range tc.codes {
				if !strings.Contains(text, code) {
					t.Fatalf("missing %s from %s; extracted: %s", code, tc.name, text)
				}
			}
		})
	}
	if _, err := svc.ExtractTextBytes("corrupt.docx", []byte("INVALID-4739")); err == nil {
		t.Fatal("corrupt office file accepted")
	}
}

func TestNotebookCitationsAcceptObservedModelFormatting(t *testing.T) {
	id, chat := uuid.New(), uuid.New()
	sources := []NotebookResearchSource{{ID: "S1", FileID: id, FileName: "source.docx", Revision: strings.Repeat("a", 64), Excerpts: []NotebookSourceExcerpt{{StartLine: 3, EndLine: 7}}}}
	for _, marker := range []string{"[S1:L3]", "[\u200bS1:L3]", "[ S1:L3 ]", "[\u00a0S1:L3\u00a0]", "【S1:L3】", "[S1:L3-L4]", "[S1:L3–L4]", "[S1:L3-4]", "`[S1:L3]`", "S1:L3", "S1:L3‑L4", "<!--\u202f[S1:L3]\u202f-->", "<!-- `[S1:L3-L4]` -->"} {
		linked := linkNotebookCitations(marker, chat, sources)
		if !strings.Contains(linked, "source="+id.String()) || !strings.Contains(linked, "start_line=3") || !strings.Contains(linked, "end_line=") || !strings.Contains(linked, "revision=") {
			t.Fatalf("observed citation is not linked: %q => %q", marker, linked)
		}
		if strings.Contains(linked, "<!--") || strings.Contains(linked, "`[") {
			t.Fatalf("citation is hidden by its formatting: %q", linked)
		}
	}
	for _, marker := range []string{"[ S1:L2 ]", "【S9:L3】", "[S1:L7–L9]", "<!-- [S9:L3] -->"} {
		if linked := linkNotebookCitations(marker, chat, sources); !strings.Contains(linked, "не подтверждена") || strings.Contains(linked, "source=") {
			t.Fatalf("unavailable excerpt linked: %q", linked)
		}
	}
	code := "```txt\n[S1:L3]\n<!-- [S1:L3] -->\n```\n~~~txt\n[S1:L3]\n~~~"
	if got := linkNotebookCitations(code, chat, sources); got != code {
		t.Fatalf("source code was modified: %s", got)
	}
}

func TestNotebookScanOCRPreservesAllPagesAndMixedPDF(t *testing.T) {
	for _, tool := range []string{"pdftotext", "pdftoppm", "tesseract"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("NOTEBOOK_REQUIRE_CONVERTERS") == "1" {
				t.Fatal(err)
			}
			t.Skip("OCR tools are not installed locally")
		}
	}
	svc := NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())
	for _, name := range []string{"DR-QA-scanned.pdf", "DR-QA-mixed.pdf"} {
		t.Run(name, func(t *testing.T) {
			buf, err := os.ReadFile("testdata/notebook/" + name)
			if err != nil {
				t.Fatal(err)
			}
			text, err := svc.ExtractTextBytes(name, buf)
			if err != nil {
				t.Fatal(err)
			}
			for _, code := range []string{"ПДФ-4628", "ПДФ-ФИНАЛ-8153"} {
				if !strings.Contains(text, code) {
					t.Fatalf("OCR lost %s: %s", code, text)
				}
			}
			first := "--- PDF: страница 1; способ извлечения: OCR изображения страницы ---"
			if name == "DR-QA-mixed.pdf" {
				first = "--- PDF: страница 1; способ извлечения: встроенный текст ---"
			}
			second := "--- PDF: страница 2; способ извлечения: OCR изображения страницы ---"
			p1, p2 := strings.Index(text, first), strings.Index(text, second)
			start, end := strings.Index(text, "ПДФ-4628"), strings.Index(text, "ПДФ-ФИНАЛ-8153")
			if p1 < 0 || p2 <= p1 || start <= p1 || start >= p2 || end <= p2 {
				t.Fatalf("page origin or code placement lost: %s", text)
			}
		})
	}
}

func TestNotebookCitationsUnwrapObservedGroupsAndValidateEveryMember(t *testing.T) {
	chat := uuid.New()
	sources := []NotebookResearchSource{{ID: "S1", FileID: uuid.New(), FileName: "mutable.docx", Revision: strings.Repeat("a", 64), Excerpts: []NotebookSourceExcerpt{{StartLine: 3, EndLine: 3}}}}
	for _, input := range []string{"`[S1:L3] [S1:L3]`", "``[S1:L3] [S1:L3]``", "prefix ```[S1:L3] [S1:L3]```", "<!--[S1:L3] [S1:L3]-->", "<!-- `[S1:L3] [S1:L3]` -->", "<!-- ``[S1:L3] [S1:L3]`` -->"} {
		got := linkNotebookCitations(input, chat, sources)
		if strings.Contains(got, "<!--") || strings.Contains(got, "`") || strings.Count(got, "](/web/drive/notebook?") != 2 {
			t.Fatalf("group remains hidden: %s", got)
		}
	}
	got := linkNotebookCitations("<!--[S1:L3] [S9:L3] [S1:L9]-->", chat, sources)
	if strings.Count(got, "](/web/drive/notebook?") != 1 || strings.Count(got, "ссылка на фрагмент не подтверждена") != 2 {
		t.Fatalf("invalid group member became a valid link: %s", got)
	}
	for _, input := range []string{"`source=[S1:L3]`", "``source=[S1:L3]``", "``[S1:L3] [S1:L3]`", "<!-- explanation [S1:L3] -->"} {
		got := linkNotebookCitations(input, chat, sources)
		if strings.HasPrefix(input, "`") && !strings.HasPrefix(got, "`") || strings.HasPrefix(input, "<!--") && !strings.HasPrefix(got, "<!--") {
			t.Fatalf("non-citation code/comment unwrapped: %s", got)
		}
	}
	got = linkNotebookCitations("``[S1:L3] [S9:L3] [S1:L9]``", chat, sources)
	if strings.Contains(got, "`") || strings.Count(got, "](/web/drive/notebook?") != 1 || strings.Count(got, "ссылка на фрагмент не подтверждена") != 2 {
		t.Fatalf("double-backtick validation bypassed: %s", got)
	}
	code := "```txt\n`[S1:L3] [S1:L3]`\n<!--[S1:L3] [S1:L3]-->\n```"
	if got := linkNotebookCitations(code, chat, sources); got != code {
		t.Fatal("fenced source code was modified")
	}
}

func TestNotebookPDFPageProvenanceDoesNotMakeEmptyPagesReadable(t *testing.T) {
	for _, pages := range [][]string{nil, {"", " \n"}} {
		if _, err := notebookPDFPageText(pages, make([]bool, len(pages))); err != errNoExtractableText {
			t.Fatalf("generated headers disguised empty content: %v", err)
		}
	}
	text, err := notebookPDFPageText([]string{"Budget 3800", "LAST-7249"}, []bool{false, true})
	if err != nil || !strings.Contains(text, "страница 1; способ извлечения: встроенный текст") || !strings.Contains(text, "страница 2; способ извлечения: OCR изображения страницы") || !strings.Contains(text, "Budget 3800") || !strings.Contains(text, "LAST-7249") {
		t.Fatalf("mixed-page facts/provenance missing: %s %v", text, err)
	}
	if _, err := notebookPDFPageText([]string{"text"}, nil); err == nil {
		t.Fatal("mismatched page metadata accepted")
	}
}

func TestNotebookPasswordAndOCRLimitsDoNotSilentlyDropPages(t *testing.T) {
	if _, err := extractNotebookPDFOCR(context.Background(), nil, make([]string, 33)); err != errNotebookOCRLimit {
		t.Fatalf("OCR limit did not reject entire document: %v", err)
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("Poppler is not installed locally")
	}
	buf, err := os.ReadFile("testdata/notebook/DR-QA-protected.pdf")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())
	if _, err := svc.ExtractTextBytes("protected.pdf", buf); err != errNotebookPDFPassword {
		t.Fatalf("password requirement was hidden: %v", err)
	}
}

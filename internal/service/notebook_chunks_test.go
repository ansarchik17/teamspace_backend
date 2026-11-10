package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func notebookNumberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("строка %04d %s", i+1, strings.Repeat("т", 40))
	}
	return lines
}

func TestNotebookChunksKeepLineNumbersAndCoverWholeText(t *testing.T) {
	lines := notebookNumberedLines(1000)
	chunks := notebookChunkText(0, strings.Join(lines, "\n"), 5000, 3)
	if len(chunks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	covered := make([]bool, len(lines))
	for i, c := range chunks {
		if c.Index != i || c.Last != (i == len(chunks)-1) {
			t.Fatalf("bad chunk order flags: %+v", c)
		}
		if c.Text != strings.Join(lines[c.StartLine-1:c.EndLine], "\n") {
			t.Fatalf("chunk %d text does not match its line range", i)
		}
		if len([]rune(c.Text)) > 5000 {
			t.Fatalf("chunk %d exceeds size", i)
		}
		if i > 0 && c.StartLine != chunks[i-1].EndLine-2 {
			t.Fatalf("expected 3 overlapping lines between chunks: %d..%d then %d", chunks[i-1].StartLine, chunks[i-1].EndLine, c.StartLine)
		}
		for l := c.StartLine; l <= c.EndLine; l++ {
			covered[l-1] = true
		}
	}
	for i, ok := range covered {
		if !ok {
			t.Fatalf("line %d not covered", i+1)
		}
	}
}

func TestNotebookChunksPreferPDFPageBoundary(t *testing.T) {
	lines := notebookNumberedLines(100)
	lines[90] = "--- PDF: страница 2; способ извлечения: текст ---"
	chunks := notebookChunkText(0, strings.Join(lines, "\n"), 95*len([]rune(lines[0]+"\n")), 0)
	if len(chunks) < 2 || chunks[1].StartLine != 91 || !notebookPDFPageMarker(strings.Split(chunks[1].Text, "\n")[0]) {
		t.Fatalf("chunk did not start at the PDF page marker: %+v", chunks[1].StartLine)
	}
}

func TestNotebookChunkRankingKeepsDocumentEdgesAndMatches(t *testing.T) {
	var chunks []notebookChunk
	for i := 0; i < 20; i++ {
		text := "обычный текст"
		if i == 11 {
			text = "здесь бюджет проекта и бюджетные лимиты"
		}
		chunks = append(chunks, notebookChunk{Source: 0, Index: i, Last: i == 19, StartLine: i*10 + 1, EndLine: i*10 + 10, Text: text})
	}
	ranked := rankNotebookChunks(chunks, "какой бюджет?", 4)
	if len(ranked) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(ranked))
	}
	want := map[int]bool{0: true, 11: true, 19: true}
	prev := -1
	for _, c := range ranked {
		delete(want, c.Index)
		if c.Index <= prev {
			t.Fatal("ranked chunks are not in document order")
		}
		prev = c.Index
	}
	if len(want) != 0 {
		t.Fatalf("missing chunks %v", want)
	}
	if got := rankNotebookChunks(chunks[:3], "бюджет", 4); len(got) != 3 {
		t.Fatal("chunks within the limit must all be kept")
	}
}

func TestNotebookTextCacheEvictsAndExpires(t *testing.T) {
	c := newNotebookTextCache(10, notebookTextCacheTTL)
	calls := 0
	extract := func(v string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { calls++; return v, nil }
	}
	if v, _ := c.load(context.Background(), "a", extract("12345")); v != "12345" {
		t.Fatal("wrong value")
	}
	c.load(context.Background(), "a", extract("xxxxx"))
	if calls != 1 {
		t.Fatal("cached value extracted twice")
	}
	c.load(context.Background(), "b", extract("123456"))
	if _, ok := c.get("a"); ok {
		t.Fatal("least recently used entry not evicted")
	}
	c.ttl = -1
	c.load(context.Background(), "c", extract("1"))
	if _, ok := c.get("c"); ok {
		t.Fatal("expired entry returned")
	}
	var nilCache *notebookTextCache
	if v, err := nilCache.load(context.Background(), "z", extract("ok")); err != nil || v != "ok" {
		t.Fatal("nil cache must extract")
	}
}

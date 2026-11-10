package service

import (
	"sort"
	"strings"
)

// Chunk limits are bounded by the per-user AI limiter (2 concurrent, 20 per
// minute): map calls plus reduce, fallback and verification must fit in one
// minute's window and in the 210-second request deadline.
const (
	notebookChunkChars     = 60000
	notebookChunkOverlap   = 3
	notebookMaxChunks      = 8
	notebookMapConcurrency = 2
	notebookMapMaxTokens   = 1500
)

// notebookChunk is a contiguous range of one source's numbered lines. Line
// numbers are the same stable numbering used by citations and Source.
type notebookChunk struct {
	Source    int
	Index     int
	Last      bool
	StartLine int
	EndLine   int
	Text      string
}

func notebookPDFPageMarker(line string) bool {
	return strings.HasPrefix(line, "--- PDF: страница ") && strings.HasSuffix(line, " ---")
}

// Cut only on line boundaries. Near the end of a window prefer a PDF page
// marker (the next chunk starts with it) or a blank line so a paragraph or a
// page is not split. A few overlapping lines keep sentences across cuts.
func notebookChunkText(source int, text string, size, overlap int) []notebookChunk {
	lines := strings.Split(text, "\n")
	var chunks []notebookChunk
	for start := 0; start < len(lines); {
		end, chars := start, 0
		for end < len(lines) {
			n := len([]rune(lines[end])) + 1
			if end > start && chars+n > size {
				break
			}
			chars += n
			end++
		}
		if end < len(lines) {
			floor := start + (end-start)*4/5
			for k := end - 1; k > floor; k-- {
				if notebookPDFPageMarker(lines[k]) {
					end = k
					break
				}
				if strings.TrimSpace(lines[k]) == "" {
					end = k + 1
					break
				}
			}
		}
		chunks = append(chunks, notebookChunk{Source: source, Index: len(chunks), StartLine: start + 1, EndLine: end, Text: strings.Join(lines[start:end], "\n")})
		if end >= len(lines) {
			break
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	if len(chunks) > 0 {
		chunks[len(chunks)-1].Last = true
	}
	return chunks
}

func notebookQueryTokens(question string) []string {
	var tokens []string
	for _, token := range notebookWord.FindAllString(strings.ToLower(question), -1) {
		r := []rune(token)
		if len(r) < 3 {
			continue
		}
		if len(r) > 5 {
			token = string(r[:5])
		}
		tokens = append(tokens, token)
	}
	return tokens
}

// rankNotebookChunks keeps every chunk when they fit. Otherwise the beginning
// and the end of each document are always analyzed, the rest by query matches.
// The result is in document order.
func rankNotebookChunks(chunks []notebookChunk, question string, limit int) []notebookChunk {
	if len(chunks) <= limit {
		return chunks
	}
	tokens := notebookQueryTokens(question)
	type scored struct {
		i, score int
		pinned   bool
	}
	ranked := make([]scored, len(chunks))
	for i, c := range chunks {
		lower := strings.ToLower(c.Text)
		score := 0
		for _, token := range tokens {
			score += strings.Count(lower, token)
		}
		ranked[i] = scored{i: i, score: score, pinned: c.Index == 0 || c.Last}
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].pinned != ranked[b].pinned {
			return ranked[a].pinned
		}
		return ranked[a].score > ranked[b].score
	})
	picked := make([]int, 0, limit)
	for _, r := range ranked[:limit] {
		picked = append(picked, r.i)
	}
	sort.Ints(picked)
	result := make([]notebookChunk, 0, limit)
	for _, i := range picked {
		result = append(result, chunks[i])
	}
	return result
}

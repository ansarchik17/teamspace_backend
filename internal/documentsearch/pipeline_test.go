package documentsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testPipeline(t *testing.T, cfg Config, handler http.HandlerFunc) *Pipeline {
	t.Helper()
	server := newTestHTTPService(handler)
	if cfg.QueryModel != "" {
		cfg.URL = server.URL
	}
	if cfg.RerankURL != "" {
		cfg.RerankURL = server.URL
	}
	p, err := NewPipeline(cfg, nil, zap.NewNop())
	require.NoError(t, err)
	p.client = server.Client()
	return p
}
func TestRetrievalPlanPreservesOriginalIdentifiers(t *testing.T) {
	p := testPipeline(t, Config{QueryModel: "local-query"}, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, false, req["stream"])
		require.Equal(t, "json", req["format"])
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": `{"query":"порядок увольнения","hypothetical":"Положение может описывать расторжение договора."}`}})
	})
	plan := p.Plan(context.Background(), "Как уйти по договору ABC-74291?")
	require.Equal(t, "Как уйти по договору ABC-74291?", plan.Original)
	require.Equal(t, plan.Original, plan.Lexical[0])
	require.Len(t, plan.Lexical, 2)
	require.Contains(t, plan.Semantic, "ABC-74291")
	require.Contains(t, plan.Semantic, "Положение может")
}
func TestRetrievalPlanFallsBackOnInvalidOrTimedOutModel(t *testing.T) {
	for _, body := range []string{`{}`, `{"message":{"content":"not JSON"}}`, `{"error":"offline"}`} {
		p := testPipeline(t, Config{QueryModel: "local-query"}, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		plan := p.Plan(context.Background(), "ABC-74291")
		require.Equal(t, []string{"ABC-74291"}, plan.Lexical)
		require.Equal(t, "ABC-74291", plan.Semantic)
	}
	p := testPipeline(t, Config{QueryModel: "local-query", QueryTimeout: time.Millisecond}, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	require.Equal(t, "original", p.Plan(context.Background(), "original").Semantic)
}
func TestRetrievalRerankingValidatesWholePermutation(t *testing.T) {
	lexical := []Passage{{ID: "a", DocumentID: "visible", Text: "first"}, {ID: "b", DocumentID: "visible", Text: "second"}}
	for _, tc := range []struct {
		name, body string
		first      string
	}{
		{"valid", `{"results":[{"index":0,"relevance_score":0.1},{"index":1,"relevance_score":0.9}]}`, "b"},
		{"duplicate", `{"results":[{"index":0,"relevance_score":0.1},{"index":0,"relevance_score":0.9}]}`, "a"},
		{"missing", `{"results":[{"index":0,"relevance_score":0.1}]}`, "a"},
		{"outside", `{"results":[{"index":0,"relevance_score":0.1},{"index":10,"relevance_score":0.9}]}`, "a"},
		{"bad score", `{"results":[{"index":0,"relevance_score":null},{"index":1,"relevance_score":0.9}]}`, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPipeline(t, Config{RerankURL: "http://local.test"}, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/rerank", r.URL.Path)
				var req struct{ Documents []string }
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				require.Equal(t, []string{"\nfirst", "\nsecond"}, req.Documents)
				_, _ = w.Write([]byte(tc.body))
			})
			ranked := p.Rank(context.Background(), "question", lexical, nil, 2)
			require.Len(t, ranked, 2)
			require.Equal(t, tc.first, ranked[0].ID)
		})
	}
}
func TestRetrievalRerankerFailureKeepsEvidence(t *testing.T) {
	p := testPipeline(t, Config{RerankURL: "http://local.test"}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	lexical := []Passage{{ID: "a", Text: "alpha"}, {ID: "b", Text: "beta"}}
	require.Equal(t, Fuse(lexical), p.Rank(context.Background(), "q", lexical, nil, 2))
}

type testSemanticRanker struct {
	indices []int
	err     error
}

func (f testSemanticRanker) RankTexts(context.Context, string, []string) ([]int, error) {
	return f.indices, f.err
}
func TestRetrievalNotebookUsesSharedRankingAndKeepsOnlySourceText(t *testing.T) {
	p, err := NewPipeline(Config{}, testSemanticRanker{indices: []int{1}}, zap.NewNop())
	require.NoError(t, err)
	indices, err := p.RankTexts(context.Background(), "company exit", []string{"unrelated", "Расторжение договора"})
	require.NoError(t, err)
	require.Equal(t, []int{1}, indices)
	p.semantic = testSemanticRanker{err: fmt.Errorf("offline")}
	indices, err = p.RankTexts(context.Background(), "ABC-74291", []string{"unrelated", "Код ABC-74291"})
	require.NoError(t, err)
	require.Equal(t, []int{1}, indices)
}
func TestRetrievalFusionDeduplicatesVotes(t *testing.T) {
	hits := Fuse([]Passage{{ID: "a"}, {ID: "a"}, {ID: "b"}}, []Passage{{ID: "b"}})
	require.Len(t, hits, 2)
	require.Equal(t, "b", hits[0].ID)
}
func TestRetrievalChunkCoordinatesAndOverlap(t *testing.T) {
	text := "\n\n" + strings.Repeat("Раздел договора\n", 100)
	runes := []rune(text)
	chunks := Split(text, 300, 40)
	require.Greater(t, len(chunks), 2)
	for i, c := range chunks {
		require.Equal(t, i, c.Ordinal)
		require.Equal(t, c.Text, string(runes[c.StartChar:c.EndChar]))
		require.LessOrEqual(t, len([]rune(c.Text)), 300)
		require.Equal(t, 1+strings.Count(string(runes[:c.StartChar]), "\n"), c.StartLine)
		require.Equal(t, 1+strings.Count(string(runes[:c.EndChar]), "\n"), c.EndLine)
		if i > 0 {
			require.Less(t, c.StartChar, chunks[i-1].EndChar)
		}
	}
}
func TestRetrievalLocalConfigurationCannotRedirectToArbitraryCredentials(t *testing.T) {
	for _, url := range []string{"file:///tmp/model", "http://user:secret@host", "http://host?token=x", "http://host#fragment"} {
		_, err := NewPipeline(Config{RerankURL: url}, nil, zap.NewNop())
		require.Error(t, err)
	}
}

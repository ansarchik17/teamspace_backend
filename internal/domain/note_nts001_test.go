package domain_test

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
)

func TestNTS001FormattingRoundTrip(t *testing.T) {
	doc := &domain.ContentDoc{Type: "doc", Version: 1, Blocks: []domain.ContentBlock{
		{ID: "h", Type: "paragraph", Runs: []domain.ContentRun{{Text: "Heading", Marks: []string{"heading1", "bold", "color:#1687FF"}}}},
		{ID: "b", Type: "paragraph", ListType: "bullet", Runs: []domain.ContentRun{{Text: "Bullet"}}},
		{ID: "o", Type: "paragraph", ListType: "ordered", Runs: []domain.ContentRun{{Text: "Ordered", Marks: []string{"italic", "strike", "color:rgb(255, 0, 0)"}}}},
		{ID: "c", Type: "checklist", Items: []domain.ChecklistBlockItem{{ID: "i", Text: "Task", Checked: true}}},
	}}
	require.NoError(t, doc.Validate())
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	var restored domain.ContentDoc
	require.NoError(t, json.Unmarshal(data, &restored))
	require.Equal(t, *doc, restored)
	require.Contains(t, restored.ExtractPlainText(), "Heading")
	require.Contains(t, restored.ExtractPlainText(), "Task")
}

func TestNTS001RejectsUnsafeColorsAndListTypes(t *testing.T) {
	for _, mark := range []string{`color:red" onmouseover="alert(1)`, "color:url(javascript:alert(1))", "color:rgb(256, 0, 0)", "color:#fff;display:none", "heading4"} {
		doc := &domain.ContentDoc{Blocks: []domain.ContentBlock{{Type: "paragraph", Runs: []domain.ContentRun{{Text: "x", Marks: []string{mark}}}}}}
		require.Error(t, doc.Validate(), mark)
	}
	doc := &domain.ContentDoc{Blocks: []domain.ContentBlock{{Type: "paragraph", ListType: "invalid", Runs: []domain.ContentRun{{Text: "x"}}}}}
	require.Error(t, doc.Validate())
	for _, color := range []string{"", "#1687FF", "#abcdef"} {
		require.True(t, domain.ValidNoteColor(color))
	}
	for _, color := range []string{"red", "#fff", "javascript:alert(1)", "#1234567"} {
		require.False(t, domain.ValidNoteColor(color))
	}
}

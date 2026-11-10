package service

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

type fakeNotebookSemantic struct{ fail bool }

func (f fakeNotebookSemantic) RankTexts(_ context.Context, _ string, texts []string) ([]int, error) {
	if f.fail {
		return nil, fmt.Errorf("offline")
	}
	ranked := []int{}
	for i, text := range texts {
		if strings.Contains(text, "Расторжение трудового договора") {
			ranked = append(ranked, i)
		}
	}
	return ranked, nil
}
func TestNotebookSemanticExcerptsKeepCitationsAndReachMiddle(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = strings.Repeat("Нейтральное описание. ", 8)
	}
	lines[100] = "Расторжение трудового договора: уведомить за месяц."
	text := strings.Join(lines, "\n")
	scores, err := notebookSemanticLines(context.Background(), fakeNotebookSemantic{}, text, "Как уйти из компании?")
	require.NoError(t, err)
	excerpts, complete := notebookExcerptsRanked(text, "Как уйти из компании?", 5500, scores)
	require.False(t, complete)
	found := false
	for _, e := range excerpts {
		require.Equal(t, strings.Join(lines[e.StartLine-1:e.EndLine], "\n"), e.Text)
		if e.StartLine <= 101 && e.EndLine >= 101 {
			found = true
		}
	}
	require.True(t, found, "semantic match in middle of document must be selected")
	_, err = notebookSemanticLines(context.Background(), fakeNotebookSemantic{fail: true}, text, "question")
	require.Error(t, err)
}
func TestNotebookShortSourceRemainsComplete(t *testing.T) {
	text := "Первая строка\nПоследняя строка"
	excerpts, complete := notebookExcerptsRanked(text, "любой вопрос", 100, map[int]float64{1: 1})
	require.True(t, complete)
	require.Equal(t, text, excerpts[0].Text)
}

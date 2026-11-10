package service

import (
	"context"
	"strings"
	"testing"
)

func TestNotebookRelativeArithmeticChecksTheVerifiedAnswerBeforePublishing(t *testing.T) {
	s, chat, ids, llm := notebookTestService("Старый резерв 100000. Новый резерв 180000.")
	llm.outputs = []string{"Draft [S1:L1]", "Резерв удвоился (с 100000 до 180000). [S1:L1]"}
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "Проверь изменение резерва", FileIDs: ids, Mode: "thinking", Format: "report"})
	if err != nil || r == nil || !strings.Contains(r.Content, "Резерв увеличился (с 100000 до 180000)") || strings.Contains(r.Content, "удвоился") || !strings.Contains(r.Content, "source=") {
		t.Fatalf("wrong verified arithmetic published: %+v %v", r, err)
	}
}

func TestNotebookRelativeArithmeticCorrectsOnlySourceSupportedExplicitClaims(t *testing.T) {
	sources := []NotebookResearchSource{{Status: "ready", Excerpts: []NotebookSourceExcerpt{{Text: "100000 180000 200000 300000 900000 999999999999999999999999 1999999999999999999999998"}}}}
	cases := []struct{ in, want string }{
		{"Резерв удвоился (с 100000 тенге до 180000 тенге).", "Резерв увеличился (с 100000 тенге до 180000 тенге)."},
		{"Сумма удвоилась (с 200000 до 100000).", "Сумма уменьшилась (с 200000 до 100000)."},
		{"Значение Утроилось (с 100000 до 100000).", "Значение Не изменилось (с 100000 до 100000)."},
		{"Резерв удвоился (с 100000 до 200000).", "Резерв удвоился (с 100000 до 200000)."},
		{"Расходы утроились (с 300000 до 900000).", "Расходы утроились (с 300000 до 900000)."},
		{"Резерв удвоился (с 100000 до 220000).", "Резерв удвоился (с 100000 до 220000)."},
		{"Резерв не удвоился (с 100000 до 180000).", "Резерв не удвоился (с 100000 до 180000)."},
		{"Значение удвоилось (с 999999999999999999999999 до 1999999999999999999999998).", "Значение удвоилось (с 999999999999999999999999 до 1999999999999999999999998)."},
		{"Резерв удвоился (с 100 000 тенге до 180 000 тенге). [S1:L2]", "Резерв увеличился (с 100 000 тенге до 180 000 тенге). [S1:L2]"},
	}
	for _, c := range cases {
		if got := correctNotebookRelativeArithmetic(c.in, sources); got != c.want {
			t.Errorf("%q: got%q want%q", c.in, got, c.want)
		}
	}
}

func TestNotebookRelativeArithmeticPreservesQuotedCodeAndUnspecifiedStatements(t *testing.T) {
	sources := []NotebookResearchSource{{Status: "ready", Excerpts: []NotebookSourceExcerpt{{Text: "100000 180000"}}}}
	for _, answer := range []string{
		"«Резерв удвоился (с 100000 до 180000)»",
		"> Резерв удвоился (с 100000 до 180000)",
		"`удвоился (с 100000 до 180000)`",
		"```text\nудвоился (с 100000 до 180000)\n```",
		"Резерв удвоился; исходное значение не указано.",
	} {
		if got := correctNotebookRelativeArithmetic(answer, sources); got != answer {
			t.Errorf("changed quotation or unspecified relation: %q", got)
		}
	}
	sources[0].Status = "unreadable"
	answer := "Резерв удвоился (с 100000 до 180000)"
	if got := correctNotebookRelativeArithmetic(answer, sources); got != answer {
		t.Fatal("used unreadable source values")
	}
}

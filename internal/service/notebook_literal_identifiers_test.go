package service

import "testing"

func TestPreserveNotebookIdentifiers(t *testing.T) {
	sources := []NotebookResearchSource{{Excerpts: []NotebookSourceExcerpt{{Text: "Начальный код ПДФ-4628. Конечный ПДФ-ФИНАЛ-8153. Код AB‑123. Дата 2026-10-09, 9-17, слово Проект-Вега."}}}}
	tests := []struct{ input, want string }{
		{"ПДФ‑4628 / ПДФ–ФИНАЛ—8153", "ПДФ-4628 / ПДФ-ФИНАЛ-8153"},
		{"`ПДФ‑4628`", "`ПДФ-4628`"},
		{"AB-123", "AB‑123"},
		{"ПДФ‑9999, пдф‑4628, XP‑123", "ПДФ‑9999, пдф‑4628, XP‑123"},
		{"2026‑10‑09, 9‑17, Проект‑Вега", "2026‑10‑09, 9‑17, Проект‑Вега"},
		{"ПДФ-4628 [S1:L3-L3]", "ПДФ-4628 [S1:L3-L3]"},
	}
	for _, test := range tests {
		if got := preserveNotebookIdentifiers(test.input, sources); got != test.want {
			t.Errorf("%q: got %q, want %q", test.input, got, test.want)
		}
	}
}

func TestPreserveNotebookIdentifiersAmbiguousAndUnselected(t *testing.T) {
	sources := []NotebookResearchSource{
		{Excerpts: []NotebookSourceExcerpt{{Text: "Код AB-123."}}},
		{Excerpts: []NotebookSourceExcerpt{{Text: "Другой код AB‑123."}}},
	}
	const answer = "AB–123 / НЕВЫБРАННЫЙ‑915"
	if got := preserveNotebookIdentifiers(answer, sources); got != answer {
		t.Fatalf("ambiguous or missing code changed: %q", got)
	}
	if got := preserveNotebookIdentifiers(answer, nil); got != answer {
		t.Fatalf("no selected excerpts must leave the answer unchanged: %q", got)
	}
}

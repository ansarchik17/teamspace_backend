package service

import (
	"strings"
	"testing"
)

func TestNotebookMermaidRepairsObservedAnonymousNodes(t *testing.T) {
	input := "```mermaid\nflowchart LR\n    \"Закупка комплектов\\n580000\" --> \"Доставка\\n60000\"\n    \"Доставка\\n60000\" --> \"Обучение\\n100000\"\n    \"Обучение\\n100000\" --> \"Запуск\\n27.10.2026\"\n```\nПроверенные ссылки [S1:L6-L7]"
	want := "```mermaid\nflowchart LR\n    DRN1[\"Закупка комплектов<br/>580000\"]\n    DRN2[\"Доставка<br/>60000\"]\n    DRN3[\"Обучение<br/>100000\"]\n    DRN4[\"Запуск<br/>27.10.2026\"]\n    DRN1 --> DRN2\n    DRN2 --> DRN3\n    DRN3 --> DRN4\n```\nПроверенные ссылки [S1:L6-L7]"
	if got := repairNotebookMermaid(input); got != want {
		t.Fatalf("graph structure or citations changed:\n%s", got)
	}
}

func TestNotebookMermaidPreservesOtherSyntaxes(t *testing.T) {
	for _, input := range []string{
		"```mermaid\nflowchart LR\n A[\"Доставка\"] --> B[\"Запуск\"]\n```",
		"```mermaid\nflowchart LR\n \"A\" --> \"B\"\n B --> C\n```",
		"```mermaid\nsequenceDiagram\n A->>B: доставка\n```",
		"```text\nflowchart LR\n \"A\" --> \"B\"\n```",
		"```mermaid\nflowchart LR\n %% comment\n \"A\" --> \"B\"\n```",
	} {
		if got := repairNotebookMermaid(input); got != input {
			t.Fatalf("unrecognized/valid graph modified: %q", got)
		}
	}
	got := repairNotebookMermaid("```mermaid\nflowchart LR\n \"<script>&value\" --> \"end\"\n```")
	if strings.Contains(got, "<script>") || !strings.Contains(got, "#60;script#62;#38;value") {
		t.Fatal("repaired label was interpreted as markup")
	}
}

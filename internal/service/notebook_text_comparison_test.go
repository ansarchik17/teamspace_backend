package service

import (
	"strings"
	"testing"
)

func comparisonPDF(id, text string) NotebookResearchSource {
	n := len(strings.Split(text, "\n"))
	return NotebookResearchSource{ID: id, FileName: id + ".pdf", Status: "ready", Complete: true, TotalLines: n, Excerpts: []NotebookSourceExcerpt{{StartLine: 1, EndLine: n, Text: text}}}
}

func TestNotebookPDFComparisonReportsVisibleDifferenceWithActualLines(t *testing.T) {
	a := comparisonPDF("S1", "--- PDF: страница 1; способ извлечения: встроенный текст ---\nReading PDF\n9 kits 17000\nCODE-4912")
	b := comparisonPDF("S2", "--- PDF: страница 1; способ извлечения: OCR изображения страницы ---\nReading POE\n9 kits 17000\nCODE-4912")
	x := compareNotebookPDFTexts([]NotebookResearchSource{a, b})
	if len(x) != 1 || x[0].Identical || x[0].DifferenceA.Line != 2 || x[0].DifferenceB.Line != 2 || x[0].DifferenceA.Text != "Reading PDF" || x[0].DifferenceB.Text != "Reading POE" {
		t.Fatalf("lost visible OCR difference: %+v", x)
	}
}

func TestNotebookPDFComparisonIgnoresWhitespaceAndExtractionMarkersOnly(t *testing.T) {
	a := comparisonPDF("S1", "--- PDF: страница 1; способ извлечения: встроенный текст ---\nReading PDF\n9 kits 17000")
	b := comparisonPDF("S2", "--- PDF: страница 1; способ извлечения: OCR изображения страницы ---\n Reading   PDF 9\nkits\t17000\n")
	x := compareNotebookPDFTexts([]NotebookResearchSource{a, b})
	if len(x) != 1 || !x[0].Identical || x[0].DifferenceA != nil || x[0].DifferenceB != nil {
		t.Fatalf("whitespace became a content difference: %+v", x)
	}
}

func TestNotebookPDFComparisonDoesNotTreatMissingPartialOrUnreadableTextAsComplete(t *testing.T) {
	a := comparisonPDF("S1", "Same text")
	for _, kind := range []string{"partial", "unreadable", "txt", "missing-excerpt"} {
		b := comparisonPDF("S2", "Other text")
		switch kind {
		case "partial":
			b.Complete = false
		case "unreadable":
			b.Status = "unreadable"
		case "txt":
			b.FileName = "test.txt"
		case "missing-excerpt":
			b.Excerpts[0].StartLine = 2
		}
		if x := compareNotebookPDFTexts([]NotebookResearchSource{a, b}); len(x) != 0 {
			t.Fatalf("%s compared as full PDF: %+v", kind, x)
		}
	}
	b := comparisonPDF("S2", "Same text extra")
	x := compareNotebookPDFTexts([]NotebookResearchSource{a, b})
	if len(x) != 1 || x[0].Identical || x[0].DifferenceA != nil || x[0].DifferenceB.Text != "Same text extra" {
		t.Fatalf("end-of-text comparison incorrect: %+v", x)
	}
}

func TestNotebookPDFComparisonBoundsEvidence(t *testing.T) {
	sources := make([]NotebookResearchSource, 32)
	for i := range sources {
		sources[i] = comparisonPDF("S", strings.Repeat("Ж", 500)+string(rune('a'+i)))
	}
	x := compareNotebookPDFTexts(sources)
	if len(x) != 20 || len([]rune(x[0].DifferenceA.Text)) != 240 || !strings.HasSuffix(x[0].DifferenceA.Text, "a") || !strings.HasSuffix(x[0].DifferenceB.Text, "b") {
		t.Fatalf("comparison evidence unbounded: %+v", x)
	}
}

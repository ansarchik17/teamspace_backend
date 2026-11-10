package service

import (
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/repository"
)

func TestSelectDocumentContextKeepsShortDocumentIntact(t *testing.T) {
	const document = "Заголовок\n\nВажный пункт: срок хранения — пять лет."

	if got := selectDocumentContext("какой срок хранения", document); got != document {
		t.Fatalf("short document was changed: %q", got)
	}
}

func TestSelectDocumentContextPrioritizesRelevantLongDocumentChunk(t *testing.T) {
	paragraphs := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		paragraphs = append(paragraphs, "Общие сведения и служебное описание раздела.")
	}
	paragraphs[23] = "Раздел о командировках: компенсация расходов выплачивается в течение пяти рабочих дней после отчёта."
	document := strings.Join(paragraphs, "\n\n")

	got := selectDocumentContext("когда выплачивается компенсация расходов", document)
	if !strings.Contains(got, "пяти рабочих дней") {
		t.Fatalf("relevant document chunk was not selected: %q", got)
	}
	if len([]rune(got)) > 32000 {
		t.Fatalf("selected context exceeds limit: %d", len([]rune(got)))
	}
}

func TestIsImageFilenameDoesNotTreatDocumentsAsImages(t *testing.T) {
	if isImageFilename("report.pdf", "application/pdf") {
		t.Fatal("PDF must go through text extraction")
	}
	if !isImageFilename("scan.png", "") {
		t.Fatal("PNG must use the vision path")
	}
}

func TestBuildDocumentPromptIncludesReadyChunksForBroadQuestion(t *testing.T) {
	got := buildDocumentPrompt([]repository.AIDocumentChunk{
		{Name: "Техническое задание.docx", Ordinal: 0, Text: "Цель проекта — автоматизировать обработку заявок."},
		{Name: "Техническое задание.docx", Ordinal: 1, Text: "Срок выполнения — 30 календарных дней."},
	})

	if !strings.Contains(got, "Цель проекта") || !strings.Contains(got, "Срок выполнения") {
		t.Fatalf("document chunks were not included in prompt: %q", got)
	}
	if !strings.Contains(got, "Техническое задание.docx") {
		t.Fatalf("document name was not included in prompt: %q", got)
	}
}

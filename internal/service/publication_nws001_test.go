package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNWS001_StatusConstants(t *testing.T) {
	if domain.PublicationStatusDraft != "draft" {
		t.Errorf("expected draft, got %s", domain.PublicationStatusDraft)
	}
	if domain.PublicationStatusPublished != "published" {
		t.Errorf("expected published, got %s", domain.PublicationStatusPublished)
	}
	if domain.PublicationStatusScheduled != "scheduled" {
		t.Errorf("expected scheduled, got %s", domain.PublicationStatusScheduled)
	}
	if domain.PublicationStatusArchived != "archived" {
		t.Errorf("expected archived, got %s", domain.PublicationStatusArchived)
	}
}

func TestNWS001_ContentDocAndHTMLParsing(t *testing.T) {
	// 1. Rich Document
	doc := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "block-1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{
					{Text: "Сегодня состоялось ", Marks: []string{}},
					{Text: "важное заседание", Marks: []string{"bold"}},
					{Text: " правительства.", Marks: []string{}},
				},
			},
		},
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("expected doc to validate, got %v", err)
	}

	plainText := strings.TrimSpace(doc.ExtractPlainText())
	expectedText := "Сегодня состоялось  важное заседание  правительства."
	if !strings.Contains(plainText, "важное заседание") {
		t.Errorf("expected plainText to contain 'важное заседание', got %q", plainText)
	}
	_ = expectedText

	// 2. HTML to plain text fallback
	htmlContent := "<p>Новая инициатива <b>министерства</b>:</p><br/><ul><li>Пункт 1</li></ul>"
	stripped := strings.TrimSpace(stripHTMLTags(htmlContent))
	if !strings.Contains(stripped, "министерства") || !strings.Contains(stripped, "Пункт 1") {
		t.Errorf("expected stripped text to contain key words, got %q", stripped)
	}
}

func TestNWS001_JSONSerialization(t *testing.T) {
	coverURL := "https://storage.example.kz/covers/news-123.jpg"
	future := time.Now().Add(24 * time.Hour)

	reqJSON := `{
		"source_type": "ministry",
		"type": "news",
		"title": "Новое постановление",
		"content": "Текст новости",
		"content_html": "<p>Текст новости</p>",
		"cover_image_url": "https://storage.example.kz/covers/news-123.jpg",
		"status": "scheduled",
		"target_audience": "all",
		"importance": "important",
		"requires_acknowledgement": false,
		"scheduled_at": "` + future.Format(time.RFC3339) + `"
	}`

	var req domain.CreatePublicationRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("failed to unmarshal CreatePublicationRequest: %v", err)
	}

	if req.Title != "Новое постановление" {
		t.Errorf("expected title 'Новое постановление', got %s", req.Title)
	}
	if req.CoverImageURL == nil || *req.CoverImageURL != coverURL {
		t.Errorf("expected cover_image_url %s, got %v", coverURL, req.CoverImageURL)
	}
	if req.Status != domain.PublicationStatusScheduled {
		t.Errorf("expected status 'scheduled', got %s", req.Status)
	}
	if req.ContentHTML != "<p>Текст новости</p>" {
		t.Errorf("expected content_html '<p>Текст новости</p>', got %s", req.ContentHTML)
	}
	if req.ScheduledAt == nil {
		t.Fatal("expected scheduled_at to be non-nil")
	}

	// Response serialization test
	pubID := uuid.New()
	now := time.Now()
	resp := domain.PublicationResponse{
		ID:            pubID,
		SourceType:    req.SourceType,
		Type:          req.Type,
		Title:         req.Title,
		Content:       req.Content,
		ContentHTML:   req.ContentHTML,
		CoverImageURL: req.CoverImageURL,
		Status:        req.Status,
		PublishedAt:   now,
		CreatedAt:     now,
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal PublicationResponse: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON map: %v", err)
	}

	if parsed["cover_image_url"] != coverURL {
		t.Errorf("expected cover_image_url in JSON, got %v", parsed["cover_image_url"])
	}
	if parsed["status"] != "scheduled" {
		t.Errorf("expected status 'scheduled', got %v", parsed["status"])
	}
	if parsed["content_html"] != "<p>Текст новости</p>" {
		t.Errorf("expected content_html in JSON, got %v", parsed["content_html"])
	}
}

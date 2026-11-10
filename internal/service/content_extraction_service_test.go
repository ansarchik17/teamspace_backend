package service

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestContentExtractionServiceWithPdfTextOnlyReturnsExtractedText(t *testing.T) {
	svc := NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())

	text, err := svc.withPdfTextOnly("Нормальный текст PDF", nil)
	if err != nil {
		t.Fatalf("withPdfTextOnly() error = %v", err)
	}
	if text != "Нормальный текст PDF" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestContentExtractionServiceWithPdfTextOnlyReturnsNoExtractableTextForEmptyResult(t *testing.T) {
	svc := NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())

	text, err := svc.withPdfTextOnly("   ", nil)
	if !errors.Is(err, ErrNoExtractableText()) {
		t.Fatalf("expected ErrNoExtractableText, got text=%q err=%v", text, err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestContentExtractionServiceWithPdfTextOnlyPropagatesExtractorError(t *testing.T) {
	svc := NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())
	expected := errors.New("pdf extractor failed")

	text, err := svc.withPdfTextOnly("", expected)
	if !errors.Is(err, expected) {
		t.Fatalf("expected extractor error, got text=%q err=%v", text, err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestContentExtractionServiceExtractTextBytesUsesDocumentParseForPdf(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != "report.pdf" {
			t.Fatalf("unexpected filename: %s", header.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chunks":[{"text":"Первая страница"},{"text":"Вторая страница"}]}`))
	}))
	defer server.Close()

	svc := NewContentExtractionService(config.DocumentParseConfig{
		URL:     server.URL,
		Timeout: 2 * time.Second,
	}, zap.NewNop())
	// Test-only HTTP fixture: production constructor rejects unaccounted remote calls.
	svc.httpClient.Transport = server.Client().Transport

	text, err := svc.ExtractTextBytes("report.pdf", []byte("%PDF-1.5"))
	if err != nil {
		t.Fatalf("ExtractTextBytes() error = %v", err)
	}
	if text != "Первая страница\n\nВторая страница" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestContentExtractionServiceExtractTextBytesReturnsParserErrorForPdf(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "parser unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	svc := NewContentExtractionService(config.DocumentParseConfig{
		URL:     server.URL,
		Timeout: 2 * time.Second,
	}, zap.NewNop())
	// Test-only HTTP fixture: production constructor rejects unaccounted remote calls.
	svc.httpClient.Transport = server.Client().Transport

	text, err := svc.ExtractTextBytes("report.pdf", []byte("%PDF-1.5"))
	if err == nil {
		t.Fatalf("expected parser error, got text=%q", text)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestContentExtractionServiceExtractTextBytesUsesDocumentParseForDocx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != "report.docx" {
			t.Fatalf("unexpected filename: %s", header.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chunks":[{"text":"Документ через document-parse"}]}`))
	}))
	defer server.Close()

	svc := NewContentExtractionService(config.DocumentParseConfig{
		URL:     server.URL,
		Timeout: 2 * time.Second,
	}, zap.NewNop())
	// Test-only HTTP fixture: production constructor rejects unaccounted remote calls.
	svc.httpClient.Transport = server.Client().Transport

	text, err := svc.ExtractTextBytes("report.docx", testZipBytes(t))
	if err != nil {
		t.Fatalf("ExtractTextBytes() error = %v", err)
	}
	if text != "Документ через document-parse" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestContentExtractionServiceExtractTextBytesUsesDocumentParseForXlsx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != "report.xlsx" {
			t.Fatalf("unexpected filename: %s", header.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chunks":[{"text":"Таблица через document-parse"}]}`))
	}))
	defer server.Close()

	svc := NewContentExtractionService(config.DocumentParseConfig{
		URL:     server.URL,
		Timeout: 2 * time.Second,
	}, zap.NewNop())
	// Test-only HTTP fixture: production constructor rejects unaccounted remote calls.
	svc.httpClient.Transport = server.Client().Transport

	text, err := svc.ExtractTextBytes("report.xlsx", testZipBytes(t))
	if err != nil {
		t.Fatalf("ExtractTextBytes() error = %v", err)
	}
	if text != "Таблица через document-parse" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestContentExtractionServiceExtractTextBytesUsesDocumentParseForPptx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != "slides.pptx" {
			t.Fatalf("unexpected filename: %s", header.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chunks":[{"text":"Слайды через document-parse"}]}`))
	}))
	defer server.Close()

	svc := NewContentExtractionService(config.DocumentParseConfig{
		URL:     server.URL,
		Timeout: 2 * time.Second,
	}, zap.NewNop())
	// Test-only HTTP fixture: production constructor rejects unaccounted remote calls.
	svc.httpClient.Transport = server.Client().Transport

	text, err := svc.ExtractTextBytes("slides.pptx", testZipBytes(t))
	if err != nil {
		t.Fatalf("ExtractTextBytes() error = %v", err)
	}
	if text != "Слайды через document-parse" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func testZipBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("content.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

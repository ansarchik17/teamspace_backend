package handler

import (
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/service"
)

func TestBuildOnlyOfficeHTMLContainsDiagnosticsAndFullConfig(t *testing.T) {
	html := buildOnlyOfficeHTML(&service.OnlyOfficeEditorConfig{
		DocumentType: "word",
		FileType:     "docx",
		DocumentKey:  "file-id-v7",
		Title:        "test onlyyy.docx",
		DocumentURL:  "https://example.com/api/v1/drive/onlyoffice/download?file_id=file-id&token=signed-token",
		CallbackURL:  "https://example.com/api/v1/drive/onlyoffice/callback?file_id=file-id",
		Mode:         "edit",
		UserID:       "32",
		UserName:     "User 32",
		CanEdit:      true,
	}, "https://example.com")

	required := []string{
		"/onlyoffice/web-apps/apps/api/documents/api.js",
		"DocsAPI.DocEditor",
		"showError",
		`id="status"`,
		"editor page loaded",
		"DocsAPI exists",
		"OnlyOffice initialization timeout",
		`"documentType":"word"`,
		`"fileType":"docx"`,
		`"key":"file-id-v7"`,
		`"url":"https://example.com/api/v1/drive/onlyoffice/download?file_id=file-id\u0026token=signed-token"`,
		`"callbackUrl":"https://example.com/api/v1/drive/onlyoffice/callback?file_id=file-id"`,
		`"id":"32"`,
		`"mode":"edit"`,
		`"edit":true`,
		`"download":true`,
		`"print":true`,
		`"fillForms":true`,
		`"coEditing":{"change":false,"mode":"fast"}`,
		`"compactToolbar":false`,
		`"toolbarNoTabs":false`,
		`"hideRightMenu":false`,
	}

	for _, needle := range required {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected HTML to contain %q\nHTML:\n%s", needle, html)
		}
	}
	if strings.Contains(html, "access_token") || strings.Contains(html, "refresh_token") {
		t.Fatalf("HTML must not contain user tokens")
	}
}

package service

import (
	"context"
	"encoding/json"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAIReaderPreservesIdentityAndRejectsImpersonation(t *testing.T) {
	reads := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer current-user" || r.Header.Get("Cookie") != "session=current" || r.Host != "acme.workspace.test" {
			t.Fatalf("identity was not preserved: %+v", r)
		}
		if r.URL.Query().Get("assignee_ids") != "42" || r.URL.Query().Get("limit") != "25" {
			t.Fatalf("unsafe filters: %v", r.URL.Query())
		}
		io.WriteString(w, `[{"id":"task-1","title":"Review","access_token":"hidden","password_hash":"hidden"}]`)
	})
	reader := NewHTTPAIUserDataReader(handler, nil)
	actor := AIDataIdentity{UserID: 42, Authorization: "Bearer current-user", Cookie: "session=current", Host: "acme.workspace.test"}
	result := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "tasks", Params: map[string]string{"mine": "true", "limit": "999"}})
	if result.Status != "ok" {
		t.Fatal(result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "hidden") {
		t.Fatalf("credentials leaked: %s", encoded)
	}
	for _, query := range []AIDataQuery{
		{Dataset: "tasks", Params: map[string]string{"user_id": "7"}},
		{Dataset: "tasks", Params: map[string]string{"assignee_ids": "7"}},
		{Dataset: "tasks", ID: "../auth/me"},
		{Dataset: "http://attacker.test"},
		{Dataset: "tasks", Params: map[string]string{"limit": "-1"}},
	} {
		if got := reader.Read(context.Background(), actor, query); got.Status != "invalid_request" {
			t.Fatalf("accepted unsafe request: %+v -> %+v", query, got)
		}
	}
	if got := reader.Read(context.Background(), AIDataIdentity{}, AIDataQuery{Dataset: "profile"}); got.Status != "forbidden" {
		t.Fatal(got)
	}
	if reads != 1 {
		t.Fatalf("rejected queries reached server: %d", reads)
	}
}

func TestAIReaderRemoteIdentityRedirectAndAccessErrors(t *testing.T) {
	status := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer limited-sd-user" || r.Host != "acme.sd.test" {
			t.Fatal("remote identity leak or substitution")
		}
		if status == 302 {
			w.Header().Set("Location", "http://127.0.0.1:1/private")
		}
		w.WriteHeader(status)
		io.WriteString(w, `{"items":[]}`)
	}))
	defer upstream.Close()
	reader := NewHTTPAIUserDataReader(nil, map[string]AIConnector{"sd": {BaseURL: upstream.URL, Authorize: func(context.Context, AIDataIdentity) (AIConnectorAuthorization, error) {
		return AIConnectorAuthorization{Token: "Bearer limited-sd-user", Host: "acme.sd.test"}, nil
	}}})
	actor := AIDataIdentity{UserID: 42, Authorization: "Bearer workspace-user", Cookie: "private-cookie"}
	for code, want := range map[int]string{200: "ok", 401: "forbidden", 403: "forbidden", 404: "not_found", 500: "unavailable", 302: "unavailable"} {
		status = code
		result := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "sd_tickets"})
		if result.Status != want {
			t.Fatalf("%d: %+v", code, result)
		}
	}
	if result := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "mail"}); result.Status != "unavailable" {
		t.Fatal(result)
	}
}

func TestAIReaderTextPaginationAndEncryptedData(t *testing.T) {
	text := strings.Repeat("Я", 8100)
	reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("text_page") {
			t.Fatal("adapter-only parameter leaked upstream")
		}
		json.NewEncoder(w).Encode(map[string]any{"body": text, "messages": []any{map[string]any{"content": "opaque-secret", "encrypted_keys": map[string]string{"device": "private"}}}, "next": "https://upstream/notes?page=2"})
	}), nil)
	actor := AIDataIdentity{UserID: 42, Authorization: "Bearer user"}
	for page, wantLen := range map[string]int{"1": 4000, "2": 4000, "3": 100} {
		got := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "notes", ID: "note-1", Params: map[string]string{"text_page": page}})
		data := got.Data.(map[string]any)
		if len([]rune(data["body"].(string))) != wantLen || got.HasMoreText != (page != "3") || !got.Truncated {
			t.Fatalf("incorrect text page: %+v", got)
		}
		raw, _ := json.Marshal(data)
		if strings.Contains(string(raw), "opaque-secret") || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "https://") {
			t.Fatalf("opaque/secret material leaked: %s", raw)
		}
		if data["has_next"] != true {
			t.Fatal("lost pagination signal")
		}
	}
}

func TestAIReaderCatalogContract(t *testing.T) {
	reader := NewHTTPAIUserDataReader(nil, nil)
	seen := map[string]bool{}
	for _, dataset := range reader.Catalog() {
		if dataset.Key == "" || seen[dataset.Key] || dataset.Description == "" {
			t.Fatalf("invalid catalog entry: %+v", dataset)
		}
		seen[dataset.Key] = true
		for _, path := range []string{dataset.Path, dataset.DetailPath} {
			if path != "" && (!strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.Contains(path, "://")) {
				t.Fatalf("unsafe catalog path: %s", path)
			}
		}
	}
	for _, key := range []string{"tasks", "events", "notes", "drive", "training", "crm_deals", "odo_documents", "sd_tickets", "mail"} {
		if !seen[key] {
			t.Fatalf("source missing: %s", key)
		}
	}
}

func TestAIReaderAttachmentRequiresScopedIDsAndPagesParsedText(t *testing.T) {
	actor := AIDataIdentity{UserID: 42, Authorization: "Bearer user"}
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v1/documents/doc-1/attachments/attachment-1/download" || r.Header.Get("Authorization") != "Bearer user" || r.URL.RawQuery != "" {
			t.Fatalf("unsafe attachment route: %+v", r)
		}
		w.Header().Set("Content-Disposition", `attachment; filename="report.txt"`)
		io.WriteString(w, strings.Repeat("Я", 7100))
	}))
	defer upstream.Close()
	reader := NewHTTPAIUserDataReader(nil, map[string]AIConnector{"odo": {BaseURL: upstream.URL}})
	reader.extractor = NewContentExtractionService(config.DocumentParseConfig{}, zap.NewNop())
	for _, id := range []string{"../attachment-1", "https://attacker.test/file", ""} {
		got := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "odo_attachment_text", ID: "doc-1", Params: map[string]string{"attachment_id": id}})
		if got.Status != "invalid_request" {
			t.Fatal(got)
		}
	}
	if hits != 0 {
		t.Fatal("invalid IDs reached upstream")
	}
	combined := ""
	for page := 1; page <= 3; page++ {
		got := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "odo_attachment_text", ID: "doc-1", Params: map[string]string{"attachment_id": "attachment-1", "text_page": strconv.Itoa(page)}})
		if got.Status != "ok" {
			t.Fatal(got)
		}
		data := got.Data.(map[string]any)
		combined += data["text"].(string)
		if data["has_more"] != (page < 3) {
			t.Fatal("incorrect attachment pagination")
		}
	}
	if combined != strings.Repeat("Я", 7100) {
		t.Fatal("attachment was truncated or repeated")
	}
}

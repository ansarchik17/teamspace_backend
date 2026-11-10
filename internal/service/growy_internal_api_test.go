package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGrowyInternalAPIKeepsHostAndStripsPublicPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/my/users/enrollments/" || r.URL.Query().Get("as_employee") != "true" || r.Host != "teamspace.kz" || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Host") != "teamspace.kz" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer growy-user" {
			t.Errorf("incorrect private Growy request: %+v", r)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("GROWY_API_INTERNAL_URL", server.URL)
	req, err := NewGrowyAPIRequest(context.Background(), http.MethodGet, "https://teamspace.kz/growy", "/api/my/users/enrollments/?as_employee=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer growy-user")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestGrowyLMSTenantContextAndConfigurationValidation(t *testing.T) {
	t.Setenv("TENANT_BASE_DOMAIN", "teamspace.kz")
	t.Setenv("GROWY_API_INTERNAL_URL", "http://crm-only:8000")
	t.Setenv("GROWY_LMS_INTERNAL_URL", "")
	request, err := NewGrowyLMSAPIRequest(t.Context(), http.MethodGet, "https://teamspace.kz/growy", "/api/training-programs/my-unfinished/", "sultan-corp", nil)
	if err != nil || request.URL.String() != "https://teamspace.kz/growy/api/training-programs/my-unfinished/" || request.Header.Get("Origin") != "https://sultan-corp.teamspace.kz" || request.Header.Get("X-Forwarded-Host") != "" {
		t.Fatalf("incorrect public LMS context: %v %+v", err, request)
	}
	for _, slug := range []string{"", "other.teamspace.kz", "../other", "evil@other", "-company", "company-", "company\r\nOrigin: evil"} {
		if _, err := NewGrowyLMSAPIRequest(t.Context(), http.MethodGet, "https://teamspace.kz/growy", "/api/my/profile/", slug, nil); err == nil {
			t.Errorf("accepted unsafe tenant slug %q", slug)
		}
	}
	for _, baseDomain := range []string{"https://evil.test", "teamspace.kz/evil", "teamspace.kz:443", ".teamspace.kz", "teamspace.kz\r\nHost: evil"} {
		t.Setenv("TENANT_BASE_DOMAIN", baseDomain)
		if _, err := NewGrowyLMSAPIRequest(t.Context(), http.MethodGet, "https://teamspace.kz/growy", "/api/my/profile/", "sultan-corp", nil); err == nil {
			t.Errorf("accepted unsafe tenant base domain")
		}
	}
	for _, base := range []string{"http://example.test/growy", "https://user:secret@teamspace.kz/growy", "https://teamspace.kz/growy?url=evil", "https://teamspace.kz/growy#evil", "file:///growy", ""} {
		t.Setenv("GROWY_LMS_PUBLIC_URL", base)
		t.Setenv("APP_PUBLIC_URL", "")
		if _, err := GrowyLMSPublicBase(); err == nil {
			t.Errorf("accepted unsafe LMS public URL")
		}
	}
}

func TestGrowyInternalAPIRejectsInvalidOriginsAndPreservesPublicFallback(t *testing.T) {
	for _, origin := range []string{"http://user:secret@growy:8000", "http://growy:8000/api", "http://growy:8000?target=evil", "http://growy:8000#fragment", "file:///private"} {
		t.Setenv("GROWY_API_INTERNAL_URL", origin)
		if _, err := NewGrowyAPIRequest(context.Background(), http.MethodPost, "https://teamspace.kz/growy", "/api/token/workspace/", nil); err == nil {
			t.Errorf("accepted invalid internal origin %q", origin)
		}
	}
	t.Setenv("GROWY_API_INTERNAL_URL", "")
	req, err := NewGrowyAPIRequest(context.Background(), http.MethodPost, "https://teamspace.kz/growy", "/api/token/workspace/", nil)
	if err != nil || req.URL.String() != "https://teamspace.kz/growy/api/token/workspace/" || req.Header.Get("X-Forwarded-Proto") != "" {
		t.Fatalf("public fallback changed: %v %+v", err, req)
	}
}

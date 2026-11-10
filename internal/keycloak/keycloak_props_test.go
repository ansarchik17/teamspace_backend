package keycloak

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseRequestErrorInvalidGrantOfflineSession(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(
			`{"error":"invalid_grant","error_description":"Offline user session not found"}`,
		)),
	}

	err := parseRequestError("refresh", resp)
	reqErr, ok := err.(*RequestError)
	if !ok {
		t.Fatalf("expected RequestError, got %T", err)
	}

	if !reqErr.IsInvalidGrant() {
		t.Fatalf("expected invalid_grant classification")
	}
	if !reqErr.IsOfflineSessionNotFound() {
		t.Fatalf("expected offline session classification")
	}
	if reqErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", reqErr.StatusCode)
	}
}

func TestParseRequestErrorPlainBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("upstream unavailable")),
	}

	err := parseRequestError("refresh", resp)
	reqErr, ok := err.(*RequestError)
	if !ok {
		t.Fatalf("expected RequestError, got %T", err)
	}

	if reqErr.ErrorCode != "" {
		t.Fatalf("expected empty structured error code, got %q", reqErr.ErrorCode)
	}
	if reqErr.Body != "upstream unavailable" {
		t.Fatalf("unexpected body: %q", reqErr.Body)
	}
}

func TestAuthCodeURLWithIdentityProviderAddsBrokerHint(t *testing.T) {
	client := NewWithPublicURL("http://keycloak:8080/auth", "https://workspace.example.com/auth", "Workspace", "workspace-client", "")
	got := client.AuthCodeURLWithIdentityProvider("https://workspace.example.com/api/v1/auth/keycloak/callback", "state-123", "workspace-acme-12345678", "user@example.com")
	if !strings.Contains(got, "kc_idp_hint=workspace-acme-12345678") {
		t.Fatalf("expected broker hint in auth URL: %s", got)
	}
	if !strings.Contains(got, "login_hint=user%40example.com") {
		t.Fatalf("expected login hint in auth URL: %s", got)
	}
}

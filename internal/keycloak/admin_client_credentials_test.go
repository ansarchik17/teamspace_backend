package keycloak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminTokenUsesClientCredentialsFlow(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/realms/GovApps/protocol/openid-connect/token" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Fatalf("grant_type = %q, want client_credentials", got)
		}
		if got := r.Form.Get("client_id"); got != "gov-workspace-admin" {
			t.Fatalf("client_id = %q, want gov-workspace-admin", got)
		}
		if got := r.Form.Get("client_secret"); got != "secret-1" {
			t.Fatalf("client_secret = %q, want secret-1", got)
		}
		if got := r.Form.Get("username"); got != "" {
			t.Fatalf("username = %q, want empty", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "admin-token",
			"expires_in":   300,
			"token_type":   "Bearer",
		})
	}))
	defer server.Close()

	client := NewAdminWithClientCredentials(server.URL, "GovApps", "gov-workspace-admin", "secret-1")
	token, err := client.AdminToken(context.Background())
	if err != nil {
		t.Fatalf("AdminToken returned error: %v", err)
	}
	if token != "admin-token" {
		t.Fatalf("token = %q, want admin-token", token)
	}
}

func TestAdminTokenFallsBackToPasswordGrant(t *testing.T) {
	t.Parallel()

	var attempts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/realms/GovApps/protocol/openid-connect/token" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		attempts = append(attempts, r.Form.Get("grant_type"))
		switch r.Form.Get("grant_type") {
		case "client_credentials":
			http.Error(w, "bad client secret", http.StatusUnauthorized)
		case "password":
			if got := r.Form.Get("client_id"); got != "admin-cli" {
				t.Fatalf("client_id = %q, want admin-cli", got)
			}
			if got := r.Form.Get("username"); got != "admin" {
				t.Fatalf("username = %q, want admin", got)
			}
			if got := r.Form.Get("password"); got != "pass-1" {
				t.Fatalf("password = %q, want pass-1", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fallback-token",
				"expires_in":   300,
				"token_type":   "Bearer",
			})
		default:
			t.Fatalf("unexpected grant_type: %s", r.Form.Get("grant_type"))
		}
	}))
	defer server.Close()

	client := NewAdminWithFallback(server.URL, "GovApps", "gov-workspace-admin", "bad-secret", "admin", "pass-1")
	token, err := client.AdminToken(context.Background())
	if err != nil {
		t.Fatalf("AdminToken returned error: %v", err)
	}
	if token != "fallback-token" {
		t.Fatalf("token = %q, want fallback-token", token)
	}
	if len(attempts) != 2 || attempts[0] != "client_credentials" || attempts[1] != "password" {
		t.Fatalf("attempts = %#v, want [client_credentials password]", attempts)
	}
}

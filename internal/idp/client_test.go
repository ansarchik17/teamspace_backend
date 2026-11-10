package idp

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestClientExchangeAuthorizationCode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tokenPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != formContentType {
			t.Fatalf("unexpected content-type header: %s", got)
		}

		authHeader := r.Header.Get("Authorization")
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("client-id:client-secret"))
		if authHeader != expected {
			t.Fatalf("unexpected authorization header: %s", authHeader)
		}

		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("unexpected grant_type: %s", got)
		}
		if got := r.Form.Get("code"); got != "auth-code" {
			t.Fatalf("unexpected code: %s", got)
		}
		if got := r.Form.Get("client_id"); got != "" {
			t.Fatalf("expected empty client_id in body, got: %s", got)
		}
		if got := r.Form.Get("redirect_uri"); got != "https://example.com/callback" {
			t.Fatalf("unexpected redirect_uri: %s", got)
		}

		w.Header().Set("Content-Type", jsonContentType)
		_, _ = w.Write([]byte(`{"access_token":"access","token_type":"bearer","refresh_token":"refresh","expires_in":1199,"scope":"user:basic:read"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "client-id", "client-secret", time.Second)
	token, err := client.ExchangeAuthorizationCode(context.Background(), "auth-code", "https://example.com/callback")
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if token.AccessToken != "access" {
		t.Fatalf("unexpected access token: %s", token.AccessToken)
	}
}

func TestClientExchangeAuthorizationCodeRetriesWithClientIDInBodyOnUnauthorized(t *testing.T) {
	t.Parallel()

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}

		switch requestCount {
		case 1:
			if got := r.Form.Get("client_id"); got != "" {
				t.Fatalf("expected no client_id on first attempt, got: %s", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`Unauthorized`))
		case 2:
			if got := r.Form.Get("client_id"); got != "client-id" {
				t.Fatalf("expected client_id on retry, got: %s", got)
			}
			w.Header().Set("Content-Type", jsonContentType)
			_, _ = w.Write([]byte(`{"access_token":"access","token_type":"bearer","refresh_token":"refresh","expires_in":1199,"scope":"user:basic:read"}`))
		default:
			t.Fatalf("unexpected extra request: %d", requestCount)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "client-id", "client-secret", time.Second)
	token, err := client.ExchangeAuthorizationCode(context.Background(), "auth-code", "https://example.com/callback")
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if token.AccessToken != "access" {
		t.Fatalf("unexpected access token: %s", token.AccessToken)
	}
	if requestCount != 2 {
		t.Fatalf("expected retry, got %d requests", requestCount)
	}
}

func TestClientExchangeAuthorizationCodeRetriesWithClientSecretPostOnRepeatedUnauthorized(t *testing.T) {
	t.Parallel()

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}

		switch requestCount {
		case 1:
			if got := r.Header.Get("Authorization"); got == "" {
				t.Fatal("expected basic auth on first attempt")
			}
			if got := r.Form.Get("client_id"); got != "" {
				t.Fatalf("expected no client_id on first attempt, got: %s", got)
			}
			if got := r.Form.Get("client_secret"); got != "" {
				t.Fatalf("expected no client_secret on first attempt, got: %s", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`Unauthorized`))
		case 2:
			if got := r.Header.Get("Authorization"); got == "" {
				t.Fatal("expected basic auth on second attempt")
			}
			if got := r.Form.Get("client_id"); got != "client-id" {
				t.Fatalf("expected client_id on second attempt, got: %s", got)
			}
			if got := r.Form.Get("client_secret"); got != "" {
				t.Fatalf("expected no client_secret on second attempt, got: %s", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`Unauthorized`))
		case 3:
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("expected no Authorization header on third attempt, got: %s", got)
			}
			if got := r.Form.Get("client_id"); got != "client-id" {
				t.Fatalf("expected client_id on third attempt, got: %s", got)
			}
			if got := r.Form.Get("client_secret"); got != "client-secret" {
				t.Fatalf("expected client_secret on third attempt, got: %s", got)
			}
			w.Header().Set("Content-Type", jsonContentType)
			_, _ = w.Write([]byte(`{"access_token":"access","token_type":"bearer","refresh_token":"refresh","expires_in":1199,"scope":"user:basic:read"}`))
		default:
			t.Fatalf("unexpected extra request: %d", requestCount)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "client-id", "client-secret", time.Second)
	token, err := client.ExchangeAuthorizationCode(context.Background(), "auth-code", "https://example.com/callback")
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if token.AccessToken != "access" {
		t.Fatalf("unexpected access token: %s", token.AccessToken)
	}
	if requestCount != 3 {
		t.Fatalf("expected 3 requests, got %d", requestCount)
	}
}

func TestClientGetUserBasicParsesAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"insufficient_scope","error_description":"Insufficient scope for this resource","scope":"user:phone:read"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "client-id", "client-secret", time.Second)
	_, err := client.GetUserBasic(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status code: %d", apiErr.StatusCode)
	}
	if apiErr.RequiredScope != "user:phone:read" {
		t.Fatalf("unexpected scope: %s", apiErr.RequiredScope)
	}
}

func TestClientAuthorizeURL(t *testing.T) {
	t.Parallel()

	client := NewClient("", "client-id", "client-secret", time.Second)
	authURL, err := client.AuthorizeURL("client-id", "https://example.com/callback", "state-1", []string{"user:basic:read", "user:phone:read"}, "kk")
	if err != nil {
		t.Fatalf("authorize url: %v", err)
	}
	if !strings.Contains(authURL, "response_type=code") {
		t.Fatalf("unexpected auth url: %s", authURL)
	}
	if !strings.Contains(authURL, "scope=user%3Abasic%3Aread+user%3Aphone%3Aread") {
		t.Fatalf("unexpected scope encoding: %s", authURL)
	}
}

func TestClientAuthorizeURLNormalizesBaseURL(t *testing.T) {
	t.Parallel()

	client := NewClient("https://idp.egov.kz/idp", "client-id", "client-secret", time.Second)
	authURL, err := client.AuthorizeURL("client-id", "https://example.com/callback", "state-1", []string{"user:basic:read"}, "kk")
	if err != nil {
		t.Fatalf("authorize url: %v", err)
	}
	if !strings.HasPrefix(authURL, "https://idp.egov.kz/idp/oauth/authorize") {
		t.Fatalf("unexpected normalized auth url: %s", authURL)
	}
}

func TestClientBaseURLOriginOnlyAndResourcePaths(t *testing.T) {
	t.Parallel()

	client := NewClient("http://test.idp.egov.kz", "client-id", "client-secret", time.Second)
	if client.baseURL != "http://test.idp.egov.kz" {
		t.Fatalf("unexpected base url: %s", client.baseURL)
	}
	if got := client.baseURL + authorizePath; got != "http://test.idp.egov.kz/idp/oauth/authorize" {
		t.Fatalf("unexpected authorize url: %s", got)
	}
	if got := client.baseURL + tokenPath; got != "http://test.idp.egov.kz/idp/oauth/token" {
		t.Fatalf("unexpected token url: %s", got)
	}
	if got := client.baseURL + userBasicPath; got != "http://test.idp.egov.kz/idp/resource/user/basic" {
		t.Fatalf("unexpected basic url: %s", got)
	}
}

func TestClientLogsNetworkTimeoutError(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"access"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "client-id", "client-secret", 10*time.Millisecond, logger)
	_, err := client.ExchangeAuthorizationCode(context.Background(), "auth-code", "https://example.com/callback")
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if logs.Len() == 0 {
		t.Fatal("expected network error log")
	}
	entry := logs.All()[0]
	if entry.Message != "idp_http_request_failed" {
		t.Fatalf("unexpected log message: %s", entry.Message)
	}
	fields := entry.ContextMap()
	if fields["network_class"] != "timeout" {
		t.Fatalf("unexpected network class: %#v", fields["network_class"])
	}
	if fields["timeout"] != true {
		t.Fatalf("expected timeout=true, got %#v", fields["timeout"])
	}
	if got := fields["request_method"]; got != http.MethodPost {
		t.Fatalf("unexpected request method: %#v", got)
	}
}

package alem

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type retryTransport func(*http.Request) (*http.Response, error)

func (fn retryTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestAccountedChatSendsExactlyOneAttempt(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := NewClient("test", time.Second, 3)
			calls := 0
			client.httpClient.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://redirect.invalid"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`)), Request: req}, nil
			})
			_, err := client.CreateChatCompletionOnce(context.Background(), ChatCompletionRequest{Model: "test", MaxTokens: 10, Messages: []Message{{Role: "user", Content: "question"}}})
			if status >= 400 && err == nil {
				t.Fatal("expected provider error")
			}
			if calls != 1 {
				t.Fatalf("one reservation sent %d HTTP attempts", calls)
			}
			if client.maxRetries != 3 || client.httpClient.CheckRedirect != nil {
				t.Fatal("shared client mutated")
			}
		})
	}
}

func TestChatResponsePreservesUsagePresence(t *testing.T) {
	for _, body := range []string{`{"model":"test"}`, `{"model":"test","usage":null}`, `{"model":"test","usage":{"prompt_tokens":0,"completion_tokens":0}}`, `{"model":"test","usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`} {
		client := NewClient("test", time.Second, 0)
		client.httpClient.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})
		resp, err := client.CreateChatCompletionOnce(context.Background(), ChatCompletionRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, "usage") && len(resp.AccountingUsage) != 0 {
			t.Fatal("absent usage became present")
		}
		if strings.Contains(body, `"total_tokens":3`) && (resp.Usage.TotalTokens != 3 || resp.Usage.PromptTokens != 2) {
			t.Fatalf("legacy counters lost: %+v", resp.Usage)
		}
	}
}

func TestClientRetriesWithCompleteBodyAndPreservesFinalError(t *testing.T) {
	for _, finalStatus := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run(http.StatusText(finalStatus), func(t *testing.T) {
			calls := 0
			client := NewClient("test", 10*time.Second, 1)
			client.httpClient.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != `{"question":"test"}` {
					t.Errorf("attempt %d body = %q, error %v", calls, body, err)
				}
				req.Body.Close()
				calls++
				status := http.StatusServiceUnavailable
				if calls == 2 {
					status = finalStatus
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"retry limit","type":"rate_limit"}}`)), Request: req}, nil
			})
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://mock.invalid", strings.NewReader(`{"question":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.do(request)
			if calls != 2 {
				t.Fatalf("got %d attempts", calls)
			}
			if finalStatus == http.StatusOK {
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
			} else {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != finalStatus || apiErr.Details.Message != "retry limit" {
					t.Fatalf("lost final upstream error: %v", err)
				}
			}
		})
	}
}

func TestClientNormalizesAuthorizationToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "raw token", token: "secret-value"},
		{name: "bearer token", token: "Bearer secret-value"},
		{name: "mixed case bearer token", token: "  bEaReR   secret-value  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got, want := r.Header.Get("Authorization"), "Bearer secret-value"; got != want {
					t.Fatalf("Authorization header = %q, want %q", got, want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			}))
			defer server.Close()

			oldBaseURL := BaseURL
			BaseURL = server.URL
			defer func() { BaseURL = oldBaseURL }()

			client := NewClient(tt.token, time.Second, 0)
			if _, err := client.ListModels(context.Background()); err != nil {
				t.Fatalf("ListModels() error = %v", err)
			}
		})
	}
}

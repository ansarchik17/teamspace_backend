package documentsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEmbedValidatesModelOutput(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"valid", `{"embeddings":[[0.5,0.25,1]]}`, false},
		{"wrong dimensions", `{"embeddings":[[1,2]]}`, true},
		{"zero vector", `{"embeddings":[[0,0,0]]}`, true},
		{"missing item", `{"embeddings":[]}`, true},
		{"extra item", `{"embeddings":[[1,0,0],[0,1,0]]}`, true},
		{"model error", `{"error":"sensitive provider details","embeddings":[[1,0,0]]}`, true},
		{"invalid JSON", `not json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newTestHTTPService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/embed" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var req struct {
					Model    string
					Input    []string
					Truncate bool
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Model != "local-model" || len(req.Input) != 1 || req.Input[0] != "Отпуск" || req.Truncate {
					t.Errorf("wrong embedding request: %+v", req)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			s := &Search{cfg: Config{URL: server.URL, Model: "local-model", Dimensions: 3}, client: server.Client()}
			result, err := s.Embed(context.Background(), []string{"Отпуск"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if err != nil && strings.Contains(err.Error(), "sensitive") {
				t.Fatal("provider details leaked")
			}
		})
	}
}

func TestEmbedFailureAndCancellation(t *testing.T) {
	server := newTestHTTPService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); _, _ = w.Write([]byte("private")) }))
	defer server.Close()
	s := &Search{cfg: Config{URL: server.URL, Dimensions: 3}, client: server.Client()}
	s.client.Timeout = time.Second
	if _, err := s.Embed(context.Background(), []string{"text"}); err == nil || !strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected error %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Embed(ctx, []string{"text"}); err == nil {
		t.Fatal("expected cancellation")
	}
	if _, err := s.Embed(context.Background(), make([]string, 17)); err == nil {
		t.Fatal("expected batch bound")
	}
}

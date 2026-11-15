package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessReflectsDatabaseFailureAndRecovery(t *testing.T) {
	available := true
	calls := 0
	router := newTestRouter(t, func(ctx context.Context) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > readinessTimeout {
			t.Fatal("database probe must have a bounded deadline")
		}
		if !available {
			return errors.New("private database host and credentials")
		}
		return nil
	})
	for _, state := range []bool{true, false, true} {
		available = state
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
		wantCode, wantStatus := http.StatusOK, "ready"
		if !state {
			wantCode, wantStatus = http.StatusServiceUnavailable, "not_ready"
		}
		if response.Code != wantCode {
			t.Fatalf("available=%v code=%d body=%s", state, response.Code, response.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != wantStatus || body["service"] != "messenger-api" || len(body) != 2 {
			t.Fatalf("unexpected response: %v", body)
		}
		if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private") {
			t.Fatal("readiness must not cache responses or expose connection details")
		}
	}
	if calls != 3 {
		t.Fatalf("expected a fresh probe per request, got %d", calls)
	}

	// A dependency outage must not make process liveness fail or query the DB.
	available = false
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK || calls != 3 {
		t.Fatalf("liveness must be independent: code=%d calls=%d", response.Code, calls)
	}
}

func TestReadinessFailsClosedWithoutDatabaseProbe(t *testing.T) {
	response := httptest.NewRecorder()
	newTestRouter(t, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured readiness returned %d", response.Code)
	}
}

func TestReadinessCancelsSlowProbe(t *testing.T) {
	router := newTestRouter(t, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	start := time.Now()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable || time.Since(start) > readinessTimeout+time.Second {
		t.Fatalf("slow probe was not bounded: code=%d elapsed=%s", response.Code, time.Since(start))
	}
}

func TestReadinessUsesRequestContext(t *testing.T) {
	type contextKey struct{}
	called := false
	router := newTestRouter(t, func(ctx context.Context) error {
		called = true
		if ctx.Value(contextKey{}) != "request-scoped" || !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("database probe lost request cancellation or values")
		}
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-scoped"))
	cancel()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx)
	router.ServeHTTP(response, request)
	if !called || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled request: called=%v code=%d", called, response.Code)
	}
}

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerHealthChecksDatabase(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"ready", nil, http.StatusOK}, {"database unavailable", errors.New("private connection details"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := WorkerHealthHandler(func(ctx context.Context) error {
				called = true
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("database check must be bounded")
				}
				return tc.err
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
			if !called || response.Code != tc.status {
				t.Fatalf("called=%v status=%d", called, response.Code)
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("health exposed connection details")
			}
		})
	}
}

func TestWorkerDoesNotExposeApplicationRoutes(t *testing.T) {
	handler := WorkerHealthHandler(func(context.Context) error { t.Fatal("unrelated route queried database"); return nil })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}

package server

import (
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/handler"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

func TestNewRouterRegistersRoutesWithoutPanic(t *testing.T) {
	cfg := &config.Config{
		App: config.AppConfig{
			Env: "test",
		},
		Security: config.SecurityConfig{
			RateLimitEnabled: false,
		},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewRouter panicked while registering routes: %v", r)
		}
	}()

	router := NewRouter(
		cfg,
		zap.NewNop(),
		&handler.AuthHandler{},
		nil,
		nil,
		nil,
		nil,
		&handler.ChatHandler{},
		nil,
		&handler.GroupHandler{},
		nil,
		nil,
		nil,
		nil,
		&handler.FileHandler{},
		&middleware.AuthMiddleware{},
		nil,
		nil,
		nil,
		nil,
		nil,
		&handler.DeviceHandler{},
		nil,
		nil,
		&handler.CalendarHandler{},
		&handler.PinAuthHandler{},
		nil,
		nil,
		nil,
		nil,
		nil,
		&handler.ReviewHandler{},
		nil,
		nil,
		nil,
		nil,
		&handler.LiveKitHandler{},
		&handler.E2EEHandler{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		&handler.ProfileSectionHandler{},
		nil,
		nil,
		nil,
		nil,
		&handler.PublicationHandler{},
		nil,
		nil,
	)
	if router == nil {
		t.Fatal("NewRouter returned nil")
	}
	for _, expected := range []struct{ method, path string }{
		{"POST", "/api/v1/publications/:id/acknowledge"},
		{"GET", "/api/v1/publications/:id/broadcast/stats"},
		{"GET", "/api/v1/publications/:id/readers/export"},
	} {
		found := false
		for _, route := range router.Routes() {
			if route.Method == expected.method && route.Path == expected.path {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing route: %s %s", expected.method, expected.path)
		}
	}

	for _, route := range router.Routes() {
		forbidden := []string{"/egov", "/ministries", "ministry-access", "ministry-rating", "state-body-users", "/users/hr"}
		for _, fragment := range forbidden {
			if strings.Contains(route.Path, fragment) {
				t.Fatalf("legacy government route is still registered: %s %s", route.Method, route.Path)
			}
		}
	}
}

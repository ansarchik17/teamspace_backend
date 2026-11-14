package server

import (
	"net/http"
	"net/http/httptest"
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
		&handler.SearchHandler{},
		&handler.FileHandler{},
		&middleware.AuthMiddleware{},
		nil,
		nil,
		nil,
		&handler.WebSocketHandler{},
		nil,
		&handler.DeviceHandler{},
		nil,
		&handler.KanbanHandler{},
		&handler.CalendarHandler{},
		&handler.PinAuthHandler{},
		&handler.DriveHandler{},
		nil,
		&handler.AlemHandler{},
		&handler.UnifiedAIHandler{},
		&handler.ReviewHandler{},
		nil,
		nil,
		nil,
		nil,
		&handler.LiveKitHandler{},
		&handler.E2EEHandler{},
		nil,
		&handler.NotebookHandler{},
		&handler.NoteHandler{},
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
		{"POST", "/api/v1/calls/start"},
		{"GET", "/api/v1/ws"},
		{"GET", "/api/v1/search/all"},
		{"GET", "/api/v1/calendar/calendars"},
		{"GET", "/api/v1/notes"},
		{"GET", "/api/v1/notebook/chats"},
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

	// Removed modules must have no registered endpoint, even for authenticated clients.
	removed := []string{"/conversations", "/messages", "/groups", "/channels", "/posts", "/chats", "/chat-commands", "/chat-themes", "/folders", "/search/messages", "/search/attachments", "/search/identical", "/calls/group/start"}
	for _, route := range router.Routes() {
		for _, prefix := range removed {
			path := "/api/v1" + prefix
			if route.Path == path || strings.HasPrefix(route.Path, path+"/") {
				t.Fatalf("removed messenger route is still registered: %s %s", route.Method, route.Path)
			}
		}
	}
	for _, path := range removed {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1"+path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("removed %s returned %d", path, response.Code)
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

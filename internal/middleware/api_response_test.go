package middleware

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func TestLegacyAPIResponsesAreLocalizedAndSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []any{
		gin.H{"error": "SQLSTATE secret", "details": "private query", "debug": "secret"},
		gin.H{"message": "provider secret", "code": "CUSTOM_ERROR", "trace": "secret"},
		gin.H{"error": gin.H{"code": "CUSTOM_ERROR", "message": "provider secret", "details": gin.H{"response": "secret"}}},
		errors.New("socket secret"),
		apperrors.InternalWithErr("provider secret", errors.New("socket secret")),
	} {
		messages := make(map[string]string)
		for _, locale := range []string{"ru", "kk"} {
			router := gin.New()
			router.Use(Localization(nil))
			router.GET("/", func(c *gin.Context) { RespondJSON(c, 500, body) })
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Accept-Language", locale)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != 500 || strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "private query") {
				t.Fatalf("unsafe response: %s", recorder.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if nested, ok := payload["error"].(map[string]any); ok {
				payload = nested
			}
			code, ok := payload["code"].(string)
			if !ok || code == "" {
				t.Fatalf("missing code: %+v", payload)
			}
			if locale == "ru" {
				messages["code"] = code
			} else if code != messages["code"] {
				t.Fatal("code changed with locale")
			}
			messages[locale], _ = payload["message"].(string)
		}
		if messages["ru"] == messages["kk"] {
			t.Fatalf("text did not change: %+v", messages)
		}
	}
}

func TestJSONResponseKeepsSuccessfulUserContent(t *testing.T) {
	router := gin.New()
	router.Use(Localization(nil))
	router.GET("/", func(c *gin.Context) {
		RespondJSON(c, 200, gin.H{"message": "ok", "text": "user SQLSTATE text", "code": "business-data"})
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "kk")
	router.ServeHTTP(recorder, req)
	if !strings.Contains(recorder.Body.String(), `"message":"ok"`) || !strings.Contains(recorder.Body.String(), "user SQLSTATE text") {
		t.Fatal("user data changed")
	}
}

func TestAbortJSONStopsRemainingHandlers(t *testing.T) {
	router := gin.New()
	router.Use(Localization(nil))
	router.Use(func(c *gin.Context) { AbortJSON(c, 403, gin.H{"error": "private access detail"}) })
	router.GET("/", func(c *gin.Context) { t.Fatal("handler ran after abort") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
	if recorder.Code != 403 || strings.Contains(recorder.Body.String(), "private access detail") {
		t.Fatalf("bad abort: %s", recorder.Body.String())
	}
}

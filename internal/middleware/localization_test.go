package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

func TestLocalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		header, profile            string
		authenticated, lookupError bool
		want                       i18n.Locale
		lookups                    int
	}{
		{"kk-KZ", "ru", true, false, i18n.Kazakh, 0},
		{"", "kk", true, false, i18n.Kazakh, 1},
		{"en", "kk", true, false, i18n.Kazakh, 1},
		{"ru;q=0.2,kk-KZ;q=0.9", "ru", true, false, i18n.Kazakh, 0},
		{"*", "kk", true, false, i18n.Kazakh, 1},
		{"invalid;q=no", "kk", true, false, i18n.Kazakh, 1},
		{"", "", true, false, i18n.Russian, 1},
		{"", "kk", true, true, i18n.Russian, 1},
		{"", "kk", false, false, i18n.Russian, 0},
	} {
		calls := 0
		router := gin.New()
		router.Use(Localization(nil))
		router.Use(func(c *gin.Context) {
			if test.authenticated {
				c.Set("user_id", int64(7))
			}
			c.Next()
		})
		router.Use(Localization(func(_ context.Context, id int64) (string, error) {
			calls++
			if id != 7 {
				t.Fatalf("wrong user: %d", id)
			}
			if test.lookupError {
				return "", errors.New("database unavailable")
			}
			return test.profile, nil
		}))
		router.GET("/", func(c *gin.Context) {
			if got := i18n.FromContext(c.Request.Context()); got != test.want {
				t.Errorf("got %q want %q", got, test.want)
			}
			if c.GetString("locale") != string(test.want) {
				t.Error("gin/context locales differ")
			}
			c.Status(204)
		})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Language", test.header)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Header().Get("Content-Language") != string(test.want) {
			t.Fatal("incorrect response locale")
		}
		if fields := response.Header().Values("Vary"); len(fields) != 1 || fields[0] != "Accept-Language" {
			t.Fatalf("Vary duplicated or missing: %v", fields)
		}
		if calls != test.lookups || response.Code != 204 {
			t.Fatalf("lookups=%d status=%d", calls, response.Code)
		}
	}
}

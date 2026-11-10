package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
)

func TestHTTPLoggingFormats(t *testing.T) {
	t.Cleanup(func() { config.SetConsoleLogMode(false) })
	gin.SetMode(gin.TestMode)
	for _, format := range []string{"console", "json"} {
		for _, tc := range []struct {
			status int
			color  string
			level  string
		}{
			{200, "32", "info"},
			{302, "34", "info"},
			{404, "33", "warn"},
			{500, "31", "error"},
		} {
			t.Run(fmt.Sprintf("%s/%d", format, tc.status), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "http.log")
				logger, err := config.NewLogger(config.LoggerConfig{Format: format, Level: "info", Output: path})
				require.NoError(t, err)
				router := gin.New()
				router.Use(RequestID(), Logger(logger))
				router.GET("/example", func(c *gin.Context) {
					c.Set("user_id", int64(9))
					c.Status(tc.status)
				})
				request := httptest.NewRequest(http.MethodGet, "/example?limit=10", nil)
				request.Header.Set("X-Request-ID", "color-test")
				router.ServeHTTP(httptest.NewRecorder(), request)
				require.NoError(t, logger.Sync())
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				line := strings.TrimSpace(string(data))
				require.NotContains(t, line, "\n")
				if format == "console" {
					require.Contains(t, line, fmt.Sprintf("\x1b[%sm[%d]\x1b[0m", tc.color, tc.status))
					require.Contains(t, line, "\x1b[36m[READ]\x1b[0m GET /example")
				} else {
					require.NotContains(t, line, `\u001b`)
					var event map[string]any
					require.NoError(t, json.Unmarshal(data, &event))
					require.Equal(t, tc.level, event["level"])
					require.Equal(t, float64(tc.status), event["status"])
					require.Equal(t, fmt.Sprintf("[%d] [READ] GET /example", tc.status), event["msg"])
				}
				var fields map[string]any
				require.NoError(t, json.Unmarshal([]byte(line[strings.Index(line, "{"):]), &fields))
				require.Equal(t, "color-test", fields["request_id"])
				require.Equal(t, float64(9), fields["user_id"])
				require.Equal(t, "limit=10", fields["query"])
			})
		}
	}
}

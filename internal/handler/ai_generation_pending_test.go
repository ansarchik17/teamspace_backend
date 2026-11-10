package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func TestAIUnconfirmedGenerationIsNotReportedAsRunning(t *testing.T) {
	for _, locale := range []string{"ru", "kk"} {
		router := gin.New()
		router.Use(middleware.Localization(nil))
		router.GET("/test", func(c *gin.Context) {
			WriteAICallError(c, &domain.AIReservationDenied{Reason: "RESERVATION_PENDING", Scope: "account"})
		})
		request := httptest.NewRequest(http.MethodGet, "/test", nil)
		request.Header.Set("Accept-Language", locale)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusTooManyRequests, response.Code)
		require.Empty(t, response.Header().Get("Retry-After"))
		var result struct {
			Code    string `json:"code"`
			Error   string `json:"error"`
			Details struct {
				RetryKind string  `json:"retry_kind"`
				RetryAt   *string `json:"retry_at"`
				Blockers  []struct {
					Reason string `json:"reason"`
					Scope  string `json:"scope"`
				} `json:"blockers"`
			} `json:"details"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Equal(t, "AI_QUOTA_EXCEEDED", result.Code)
		require.Equal(t, "unknown", result.Details.RetryKind)
		require.Nil(t, result.Details.RetryAt)
		require.Len(t, result.Details.Blockers, 1)
		require.Equal(t, "RESERVATION_PENDING", result.Details.Blockers[0].Reason)
		require.Equal(t, "account", result.Details.Blockers[0].Scope)
		if locale == "ru" {
			require.Contains(t, result.Error, "Не удалось подтвердить завершение")
		} else {
			require.Contains(t, result.Error, "аяқталғанын растау мүмкін болмады")
		}
	}
}

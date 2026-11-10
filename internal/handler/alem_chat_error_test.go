package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func TestAlemChatErrorsKeepInternalDetailsOutOfUserResponse(t *testing.T) {
	for _, item := range []struct {
		err    error
		status int
	}{
		{errors.New("database failed: SECRET connection string"), http.StatusBadGateway},
		{fmt.Errorf("wrapped: %w", &alem.APIError{StatusCode: 401, Details: alem.ErrorDetails{Message: "SECRET provider config"}}), http.StatusBadGateway},
		{&alem.APIError{StatusCode: 429, Details: alem.ErrorDetails{Message: "SECRET rate limit"}}, http.StatusServiceUnavailable},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{errors.New("unauthorized conversation"), http.StatusForbidden},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeAlemChatError(c, item.err)
		if w.Code != item.status || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "provider") || strings.Contains(w.Body.String(), "database") {
			t.Fatalf("unsafe user error: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestAlemChatAccountingErrorsAreLocalized(t *testing.T) {
	for _, locale := range []string{"ru", "kk"} {
		for _, tc := range []struct {
			err    error
			status int
			code   string
		}{
			{&domain.AIReservationDenied{Reason: "USER_WEEKLY_EXHAUSTED", Scope: "weekly"}, 429, string(apperrors.ErrCodeAIQuotaExceeded)},
			{&domain.AIReservationDenied{Reason: "USER_QUOTA_UNCONFIGURED", Scope: "weekly"}, 403, string(apperrors.ErrCodeAIAccessDisabled)},
			{&domain.AIReservationDenied{Reason: "AI_ACCESS_DENIED", Scope: "account"}, 403, string(apperrors.ErrCodeAIAccessDisabled)},
			{domain.ErrAIPaidCallContextMismatch, 403, string(apperrors.ErrCodeAIAccessDisabled)},
			{domain.ErrAIRateLimitExceeded, 429, string(apperrors.ErrCodeAIRateLimitExceeded)},
			{domain.ErrAIConcurrencyLimitExceeded, 429, string(apperrors.ErrCodeAIConcurrencyLimitExceeded)},
			{domain.ErrAILimiterUnavailable, 503, string(apperrors.ErrCodeAILimiterUnavailable)},
		} {
			router := gin.New()
			router.Use(middleware.Localization(nil))
			router.GET("/", func(c *gin.Context) { writeAlemChatError(c, tc.err) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Accept-Language", locale)
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.status, w.Code, w.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json: %s", w.Body.String())
			}
			if resp["code"] != tc.code {
				t.Fatalf("expected code %s, got %s", tc.code, resp["code"])
			}
			errMsg, _ := resp["error"].(string)
			if strings.Contains(errMsg, "USER_QUOTA") || strings.Contains(errMsg, "AI_ACCESS") || strings.Contains(errMsg, "SECRET") {
				t.Fatalf("unsafe accounting message in user error: %s", errMsg)
			}
			if locale == "kk" && !strings.Contains(w.Body.String(), "ЖИ") {
				t.Fatalf("missing Kazakh translation: %s", w.Body.String())
			}
		}
	}
}

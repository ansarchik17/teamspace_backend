package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func TestAIErrorResponseContract_AllAccessDisabledReasons(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reasons := []struct {
		reason    string
		wantScope string
	}{
		{"NO_ORGANIZATION", "account"},
		{"COMPANY_SUSPENDED", "company"},
		{"AI_ACCESS_DENIED", "account"},
		{"COMPANY_BUDGET_UNCONFIGURED", "company"},
		{"COMPANY_BUDGET_DISABLED", "company"},
		{"USER_QUOTA_UNCONFIGURED", "account"},
		{"USER_QUOTA_DISABLED", "weekly"},
		{"FIVE_HOUR_DISABLED", "five_hour"},
		{"SERVICE_QUOTA_DISABLED", "service"},
		{"CREDIT_POLICY_UNAVAILABLE", "policy"},
	}

	for _, tc := range reasons {
		t.Run(tc.reason, func(t *testing.T) {
			for _, loc := range []string{"ru", "kk"} {
				router := gin.New()
				router.Use(middleware.Localization(nil))
				router.GET("/test", func(c *gin.Context) {
					WriteAICallError(c, &domain.AIReservationDenied{
						Reason: tc.reason,
					})
				})

				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				req.Header.Set("Accept-Language", loc)
				router.ServeHTTP(w, req)

				require.Equal(t, http.StatusForbidden, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				require.Empty(t, w.Header().Get("Retry-After"), "Retry-After must never be present on 403 access disabled")

				var resp map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				require.Equal(t, string(apperrors.ErrCodeAIAccessDisabled), resp["code"])

				errMsg, ok := resp["error"].(string)
				require.True(t, ok)
				require.NotEmpty(t, errMsg)
				require.False(t, strings.Contains(errMsg, tc.reason), "error message must be human-readable, not raw code")

				details, ok := resp["details"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, tc.reason, details["reason"])
				require.Equal(t, tc.wantScope, details["scope"])
			}
		})
	}
}

func TestAIErrorResponseContract_QuotaExceededSemanticsAndRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	future := time.Now().UTC().Add(45 * time.Second)

	tests := []struct {
		name           string
		denied         *domain.AIReservationDenied
		wantRetryKind  string
		wantRetryAfter bool
	}{
		{
			name: "5-hour exhausted with estimated reset",
			denied: &domain.AIReservationDenied{
				Reason:    "FIVE_HOUR_EXHAUSTED",
				Scope:     "five_hour",
				RecheckAt: &future,
			},
			wantRetryKind:  "estimated",
			wantRetryAfter: true,
		},
		{
			name: "user weekly exhausted with estimated reset",
			denied: &domain.AIReservationDenied{
				Reason:    "USER_WEEKLY_EXHAUSTED",
				Scope:     "weekly",
				RecheckAt: &future,
			},
			wantRetryKind:  "estimated",
			wantRetryAfter: true,
		},
		{
			name: "company weekly exhausted without reset (requires action)",
			denied: &domain.AIReservationDenied{
				Reason:    "COMPANY_WEEKLY_EXHAUSTED",
				Scope:     "company",
				RecheckAt: nil,
			},
			wantRetryKind:  "requires_action",
			wantRetryAfter: false,
		},
		{
			name: "reservation pending (unknown retry kind)",
			denied: &domain.AIReservationDenied{
				Reason:    "RESERVATION_PENDING",
				Scope:     "weekly",
				RecheckAt: nil,
			},
			wantRetryKind:  "unknown",
			wantRetryAfter: false,
		},
		{
			name: "request exceeds full quota even after reset",
			denied: &domain.AIReservationDenied{
				Reason: "RESERVATION_TOO_LARGE", Scope: "five_hour", RecheckAt: &future,
				AvailableMicrocredits: new(int64),
			},
			wantRetryKind: "requires_action",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(middleware.Localization(nil))
			router.GET("/test", func(c *gin.Context) {
				WriteAICallError(c, tc.denied)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Accept-Language", "ru")
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusTooManyRequests, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))

			if tc.wantRetryAfter {
				retryAfterHeader := w.Header().Get("Retry-After")
				require.NotEmpty(t, retryAfterHeader)
				secs, err := strconv.Atoi(retryAfterHeader)
				require.NoError(t, err)
				require.True(t, secs > 0 && secs <= 46, "Retry-After should be approximately 45 seconds")
			} else {
				require.Empty(t, w.Header().Get("Retry-After"), "Retry-After header must be omitted when retry_kind is not estimated")
			}

			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, string(apperrors.ErrCodeAIQuotaExceeded), resp["code"])

			details, ok := resp["details"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tc.wantRetryKind, details["retry_kind"])
			require.NotEmpty(t, details["server_time"])
			if tc.wantRetryKind == "requires_action" {
				require.Nil(t, details["retry_at"])
				require.Nil(t, details["recheck_at"])
			}
			require.NotContains(t, w.Body.String(), "available_microcredits")
			require.NotContains(t, w.Body.String(), "AvailableMicrocredits")

			blockers, ok := details["blockers"].([]any)
			require.True(t, ok)
			require.Len(t, blockers, 1)

			blocker, ok := blockers[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tc.denied.Reason, blocker["reason"])
			require.Equal(t, tc.denied.Scope, blocker["scope"])
			require.Equal(t, true, blocker["enforced"])

			// Security check: make sure NO company budget amounts or microcredits leak in blocker
			require.Nil(t, blocker["limit_microcredits"])
			require.Nil(t, blocker["spent_microcredits"])
			require.Nil(t, blocker["reserved_microcredits"])
			require.Nil(t, blocker["balance"])
		})
	}
}

func TestAIErrorResponseContract_CallerAndContextErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	callerErrors := []error{
		domain.ErrAIPaidCallMissingContext,
		domain.ErrAIPaidCallInvalidCaller,
		domain.ErrAIPaidCallContextMismatch,
	}

	for _, err := range callerErrors {
		t.Run(err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			WriteAICallError(c, err)

			require.Equal(t, http.StatusForbidden, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, string(apperrors.ErrCodeAIAccessDisabled), resp["code"])
			details := resp["details"].(map[string]any)
			require.Equal(t, "AI_ACCESS_DENIED", details["reason"])
			require.Equal(t, "account", details["scope"])
		})
	}
}

func TestAIErrorResponseContract_RateAndConcurrencyLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{domain.ErrAIRateLimitExceeded, http.StatusTooManyRequests, string(apperrors.ErrCodeAIRateLimitExceeded)},
		{domain.ErrAIConcurrencyLimitExceeded, http.StatusTooManyRequests, string(apperrors.ErrCodeAIConcurrencyLimitExceeded)},
		{domain.ErrAILimiterUnavailable, http.StatusServiceUnavailable, string(apperrors.ErrCodeAILimiterUnavailable)},
	}

	for _, tc := range tests {
		t.Run(tc.wantCode, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			WriteAICallError(c, tc.err)

			require.Equal(t, tc.wantStatus, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, tc.wantCode, resp["code"])
			require.NotEmpty(t, resp["error"])
		})
	}
}

func TestAIErrorResponseContract_OperationConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	WriteAICallError(c, domain.ErrAIReservationConflict)

	require.Equal(t, http.StatusConflict, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, string(apperrors.ErrCodeAIReservationConflict), resp["code"])
	details := resp["details"].(map[string]any)
	require.Equal(t, "OPERATION_CONFLICT", details["reason"])
	require.Empty(t, w.Header().Get("Retry-After"), "conflict must never have Retry-After to prevent automatic retry")
}

func TestAIErrorResponseContract_AccountingAndPricingErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		err      error
		wantCode string
	}{
		{domain.ErrAICreditUnknownModel, string(apperrors.ErrCodeAIModelUnpriced)},
		{domain.ErrAICreditUnknownPolicy, string(apperrors.ErrCodeAIAccountingUnavailable)},
	}

	for _, tc := range tests {
		t.Run(tc.wantCode, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			WriteAICallError(c, tc.err)

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, tc.wantCode, resp["code"])
		})
	}
}

func TestAIErrorResponseContract_UpstreamProviderErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("provider 429 maps to 503 service unavailable, not 429 quota", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		WriteAICallError(c, &alem.APIError{StatusCode: http.StatusTooManyRequests, Details: alem.ErrorDetails{Message: "rate limit from upstream provider"}})

		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, string(apperrors.ErrCodeInternal), resp["code"])
		// Must not leak upstream details
		require.False(t, strings.Contains(w.Body.String(), "upstream"))
		require.False(t, strings.Contains(w.Body.String(), "provider"))
	})

	t.Run("provider 500 maps to 502 bad gateway", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		WriteAICallError(c, &alem.APIError{StatusCode: http.StatusInternalServerError, Details: alem.ErrorDetails{Message: "internal provider explosion"}})

		require.Equal(t, http.StatusBadGateway, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, string(apperrors.ErrCodeInternal), resp["code"])
		require.False(t, strings.Contains(w.Body.String(), "explosion"))
	})

	t.Run("deadline exceeded maps to 504 gateway timeout", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		WriteAICallError(c, context.DeadlineExceeded)

		require.Equal(t, http.StatusGatewayTimeout, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, string(apperrors.ErrCodeInternal), resp["code"])
	})
}

func TestAIErrorResponseContract_IsAICallError(t *testing.T) {
	require.True(t, IsAICallError(&domain.AIReservationDenied{Reason: "USER_WEEKLY_EXHAUSTED"}))
	require.True(t, IsAICallError(domain.ErrAIPaidCallMissingContext))
	require.True(t, IsAICallError(domain.ErrAIPaidCallInvalidCaller))
	require.True(t, IsAICallError(domain.ErrAIPaidCallContextMismatch))
	require.True(t, IsAICallError(domain.ErrAIRateLimitExceeded))
	require.True(t, IsAICallError(domain.ErrAIConcurrencyLimitExceeded))
	require.True(t, IsAICallError(domain.ErrAILimiterUnavailable))
	require.True(t, IsAICallError(domain.ErrAIReservationConflict))
	require.True(t, IsAICallError(domain.ErrAICreditUnknownModel))
	require.True(t, IsAICallError(domain.ErrAICreditUnknownPolicy))
	require.True(t, IsAICallError(&alem.APIError{StatusCode: 500}))
	require.True(t, IsAICallError(context.DeadlineExceeded))

	require.False(t, IsAICallError(nil))
	require.False(t, IsAICallError(errors.New("some arbitrary error")))
	require.False(t, IsAICallError(fmt.Errorf("sql: no rows in result set")))
}

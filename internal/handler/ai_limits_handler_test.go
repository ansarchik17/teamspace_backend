package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

type mockUserAILimitsService struct {
	getUserAILimitsFn func(ctx context.Context, userID int64) (*domain.UserAILimits, error)
}

func (m *mockUserAILimitsService) GetUserAILimits(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
	if m.getUserAILimitsFn != nil {
		return m.getUserAILimitsFn(ctx, userID)
	}
	return nil, errors.New("not implemented")
}

func setupAILimitsRouter(h *AlemHandler, actor int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if actor > 0 {
			c.Set("user_id", actor)
		}
		c.Next()
	}, middleware.Localization(nil))
	v1 := r.Group("/api/v1")
	protected := v1.Group("")
	ai := protected.Group("/ai")
	ai.GET("/limits", h.GetAILimits)
	return r
}

func TestGetAILimits_Unauthorized(t *testing.T) {
	h := NewAlemHandler(nil, zap.NewNop())
	r := setupAILimitsRouter(h, 0) // No actor set

	req := httptest.NewRequest("GET", "/api/v1/ai/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestGetAILimits_ServiceUnavailable(t *testing.T) {
	h := NewAlemHandler(nil, zap.NewNop()) // limitsService is nil
	r := setupAILimitsRouter(h, 42)

	req := httptest.NewRequest("GET", "/api/v1/ai/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, string(apperrors.ErrCodeAIAccountingUnavailable), resp["code"])
}

func TestGetAILimits_AccountingDisabled(t *testing.T) {
	mockSvc := &mockUserAILimitsService{
		getUserAILimitsFn: func(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
			return nil, domain.ErrAIAccountingDisabled
		},
	}
	h := NewAlemHandler(nil, zap.NewNop())
	h.SetLimitsService(mockSvc)
	r := setupAILimitsRouter(h, 42)

	req := httptest.NewRequest("GET", "/api/v1/ai/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, string(apperrors.ErrCodeAIAccountingUnavailable), resp["code"])
}

func TestGetAILimits_UserNotFound(t *testing.T) {
	mockSvc := &mockUserAILimitsService{
		getUserAILimitsFn: func(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
			return nil, apperrors.NotFound("user")
		},
	}
	h := NewAlemHandler(nil, zap.NewNop())
	h.SetLimitsService(mockSvc)
	r := setupAILimitsRouter(h, 42)

	req := httptest.NewRequest("GET", "/api/v1/ai/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAILimits_InternalErrorHidden(t *testing.T) {
	mockSvc := &mockUserAILimitsService{
		getUserAILimitsFn: func(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
			return nil, errors.New("raw postgres connection error: password=secret")
		},
	}
	h := NewAlemHandler(nil, zap.NewNop())
	h.SetLimitsService(mockSvc)
	r := setupAILimitsRouter(h, 42)

	req := httptest.NewRequest("GET", "/api/v1/ai/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "password=secret")
	require.NotContains(t, w.Body.String(), "raw postgres")
}

func TestGetAILimits_UserIsolation_IgnoresQueryParams(t *testing.T) {
	var requestedUserID int64
	mockSvc := &mockUserAILimitsService{
		getUserAILimitsFn: func(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
			requestedUserID = userID
			now := time.Now().UTC()
			policy := domain.AICreditPolicyV1
			return &domain.UserAILimits{
				ServerTime:          now,
				EnforcementMode:     "observe",
				CreditPolicyVersion: &policy,
				FiveHour:            domain.AIQuotaWindow{State: "unconfigured"},
				Weekly:              domain.AIQuotaWindow{State: "unconfigured"},
				Access: domain.AIAccessState{
					CanStartRequest: true,
					Blockers:        []domain.AIBlocker{},
					RetryKind:       "none",
				},
			}, nil
		},
	}
	h := NewAlemHandler(nil, zap.NewNop())
	h.SetLimitsService(mockSvc)
	r := setupAILimitsRouter(h, 42)

	// User attempts to provide another user's ID via query parameter
	req := httptest.NewRequest("GET", "/api/v1/ai/limits?user_id=999&actor_id=888", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// Server MUST only query the authenticated actor (42), completely ignoring ?user_id=999
	require.Equal(t, int64(42), requestedUserID)
}

func TestGetAILimits_ContractSuccessFormat(t *testing.T) {
	orgID := uuid.MustParse("f4673508-34cb-4dc7-9c17-f664271eb50f")
	fivePeriodID := uuid.MustParse("32ef21c9-155e-42f8-a2de-2be807ba072e")
	weekPeriodID := uuid.MustParse("4bdeec70-7294-4c1c-96a7-f49109329981")
	now := time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	fiveStart := time.Date(2026, 10, 16, 5, 20, 0, 0, time.UTC)
	fiveEnd := time.Date(2026, 10, 16, 10, 20, 0, 0, time.UTC)
	weekStart := time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)
	weekEnd := time.Date(2026, 10, 23, 5, 0, 0, 0, time.UTC)

	fiveLimit := int64(100000000)
	fiveAvail := int64(70000000)
	fivePercent := 70

	weekLimit := int64(1000000000)
	weekAvail := int64(970000000)
	weekPercent := 97

	policy := domain.AICreditPolicyV1

	mockSvc := &mockUserAILimitsService{
		getUserAILimitsFn: func(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
			return &domain.UserAILimits{
				ServerTime:          now,
				OrganizationID:      &orgID,
				EnforcementMode:     "observe",
				CreditPolicyVersion: &policy,
				FiveHour: domain.AIQuotaWindow{
					State:            "active",
					Limit:            &fiveLimit,
					Used:             30000000,
					Reserved:         0,
					CarriedReserved:  0,
					Available:        &fiveAvail,
					RemainingPercent: &fivePercent,
					PeriodID:         &fivePeriodID,
					Window:           &domain.AIPeriodWindow{StartsAt: fiveStart, EndsAt: fiveEnd},
				},
				Weekly: domain.AIQuotaWindow{
					State:            "active",
					Limit:            &weekLimit,
					Used:             30000000,
					Reserved:         0,
					CarriedReserved:  0,
					Available:        &weekAvail,
					RemainingPercent: &weekPercent,
					PeriodID:         &weekPeriodID,
					Window:           &domain.AIPeriodWindow{StartsAt: weekStart, EndsAt: weekEnd},
				},
				Access: domain.AIAccessState{
					CanStartRequest: true,
					Blockers:        []domain.AIBlocker{},
					RetryKind:       "none",
					RetryAt:         nil,
					RecheckAt:       nil,
				},
			}, nil
		},
	}

	h := NewAlemHandler(nil, zap.NewNop())
	h.SetLimitsService(mockSvc)
	r := setupAILimitsRouter(h, 42)

	req := httptest.NewRequest("GET", "/api/v1/ai/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.Equal(t, "2026-10-16T06:00:00Z", body["server_time"])
	require.Equal(t, orgID.String(), body["organization_id"])
	require.Equal(t, "observe", body["enforcement_mode"])
	require.Equal(t, "credits-v1", body["credit_policy_version"])

	// 5-hour window
	fiveHour, ok := body["five_hour"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "active", fiveHour["state"])
	require.Equal(t, "100000000", fiveHour["limit_microcredits"])
	require.Equal(t, "30000000", fiveHour["used_microcredits"])
	require.Equal(t, "0", fiveHour["reserved_microcredits"])
	require.Equal(t, "0", fiveHour["carried_reserved_microcredits"])
	require.Equal(t, "70000000", fiveHour["available_microcredits"])
	require.Equal(t, float64(70), fiveHour["remaining_percent"])

	fivePeriod, ok := fiveHour["period"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, fivePeriodID.String(), fivePeriod["id"])
	require.Equal(t, "2026-10-16T05:20:00Z", fivePeriod["starts_at"])
	require.Equal(t, "2026-10-16T10:20:00Z", fivePeriod["ends_at"])

	// Weekly window
	weekly, ok := body["weekly"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "active", weekly["state"])
	require.Equal(t, "1000000000", weekly["limit_microcredits"])
	require.Equal(t, "30000000", weekly["used_microcredits"])
	require.Equal(t, "0", weekly["reserved_microcredits"])
	require.Equal(t, "0", weekly["carried_reserved_microcredits"])
	require.Equal(t, "970000000", weekly["available_microcredits"])
	require.Equal(t, float64(97), weekly["remaining_percent"])

	weekPeriod, ok := weekly["period"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, weekPeriodID.String(), weekPeriod["id"])
	require.Equal(t, "2026-10-16T05:00:00Z", weekPeriod["starts_at"])
	require.Equal(t, "2026-10-23T05:00:00Z", weekPeriod["ends_at"])

	// Access
	access, ok := body["access"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, access["can_start_request"])
	require.Equal(t, "none", access["retry_kind"])
	require.Nil(t, access["retry_at"])
	require.Nil(t, access["recheck_at"])
	blockers, ok := access["blockers"].([]any)
	require.True(t, ok)
	require.Empty(t, blockers)
}

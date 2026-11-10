package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

func aiTestRouter(h *OrganizationHandler, actor int64) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if actor > 0 {
			c.Set("user_id", actor)
		}
		c.Next()
	}, middleware.Localization(nil))
	h.RegisterAIBudgetRoutes(r.Group("/api/v1/organizations/current/administration"), false)
	h.RegisterAIBudgetRoutes(r.Group("/api/v1/platform/organizations/:organization_id/administration"), true)
	r.GET("/api/v1/platform/ai/users/:user_id/quota", h.PlatformPersonalAIQuota)
	r.PUT("/api/v1/platform/ai/users/:user_id/quota", h.PlatformPersonalAIQuota)
	return r
}

func aiTestRequest(r http.Handler, method, path, body, locale string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", locale)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func aiErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Error.Message)
	return payload.Error.Code
}

func TestAIAdministrationValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOrganizationHandler(nil, zap.NewNop())
	r := aiTestRouter(h, 1)
	budgetPath := "/api/v1/platform/organizations/" + uuid.NewString() + "/administration/ai-budget"
	valid := `{"weekly_budget_microcredits":"1000","max_five_hour_microcredits":"100","service_weekly_quota_microcredits":"0","revision":"0"}`
	bad := []string{"", `null`, `[]`, `{}`, valid + ` {}`, valid + ` trailing`, strings.Replace(valid, `"revision":"0"`, `"revision":"0","revision":"0"`, 1), strings.Replace(valid, `"revision":"0"`, `"revision":"0","organization_id":"other"`, 1), strings.Replace(valid, `"revision"`, `"Revision"`, 1), strings.Repeat(" ", 4097) + valid}
	for _, value := range []string{`0`, `null`, `false`, `""`, `"00"`, `"+1"`, `"-1"`, `" 1"`, `"1.0"`, `"1e3"`, `"9223372036854775808"`} {
		bad = append(bad, strings.Replace(valid, `"revision":"0"`, `"revision":`+value, 1))
	}
	bad = append(bad, strings.Replace(valid, `"1000"`, `"1000000000000001"`, 1))
	for _, body := range bad {
		w := aiTestRequest(r, "PUT", budgetPath, body, "ru")
		require.Equal(t, 400, w.Code, body)
		require.Equal(t, "VALIDATION_ERROR", aiErrorCode(t, w))
	}
	quotaPath := "/api/v1/organizations/current/administration/ai-budget/users/3"
	for _, body := range []string{`{}`, `{"weekly_quota_microcredits":"100","five_hour_limit_microcredits":"50","revision":"0","company_revision":1}`, `{"weekly_quota_microcredits":"100","five_hour_limit_microcredits":"50","revision":"0","company_revision":"1","revision":"0"}`} {
		w := aiTestRequest(r, "PUT", quotaPath, body, "kk")
		require.Equal(t, 400, w.Code)
		require.Equal(t, "VALIDATION_ERROR", aiErrorCode(t, w))
		require.Equal(t, "kk", w.Header().Get("Content-Language"))
	}
	base := "/api/v1/organizations/current/administration/ai-budget/users"
	for _, query := range []string{"?limit=0", "?limit=101", "?offset=-1", "?offset=2147483648", "?limit=", "?limit=1&limit=2", "?q=a&q=b", "?limit=%XX", "?q=" + strings.Repeat("a", 256)} {
		w := aiTestRequest(r, "GET", base+query, "", "ru")
		require.Equal(t, 400, w.Code, query)
	}
	for _, id := range []string{"0", "-1", "abc", "9223372036854775808", "01"} {
		w := aiTestRequest(r, "GET", base+"/"+id, "", "ru")
		require.Equal(t, 400, w.Code, id)
	}
	w := aiTestRequest(r, "GET", "/api/v1/platform/organizations/invalid/administration/ai-budget", "", "ru")
	require.Equal(t, 400, w.Code)
	w = aiTestRequest(aiTestRouter(h, 0), "GET", base, "", "ru")
	require.Equal(t, 401, w.Code)
	w = aiTestRequest(r, "PUT", "/api/v1/organizations/current/administration/ai-budget", valid, "ru")
	require.Equal(t, 404, w.Code, "tenant budget PUT is not registered")
	req := httptest.NewRequest("PUT", budgetPath, strings.NewReader(valid))
	req.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
}

func TestAIBudgetSettingsWithoutServiceAllocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"weekly_budget_microcredits":"1000","max_five_hour_microcredits":"100","revision":"0"}`,
		`{"weekly_budget_microcredits":"1000","max_five_hour_microcredits":"100","revision":"0","service_weekly_quota_microcredits":"0"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		values, err := aiSettingsBody(c, []string{"weekly_budget_microcredits", "max_five_hour_microcredits", "revision"}, "service_weekly_quota_microcredits")
		require.NoError(t, err)
		require.Equal(t, int64(1000), values["weekly_budget_microcredits"])
	}
}

func TestAIAdministrationPayloadPrecision(t *testing.T) {
	value := int64(math.MaxInt64)
	revision := int64(9007199254740993)
	out := &domain.AIBudgetSummary{Revision: revision, WeeklyBudget: &value, Used: value, Configured: true}
	data, err := json.Marshal(aiBudgetResponse(out))
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	require.Equal(t, "9223372036854775807", payload["used_microcredits"])
	require.Equal(t, "9007199254740993", payload["revision"])
	require.Nil(t, payload["period"])
	require.Nil(t, payload["max_five_hour_microcredits"])
	window := &domain.AIPeriodWindow{StartsAt: time.Date(2026, 10, 19, 0, 0, 0, 123456000, time.UTC), EndsAt: time.Date(2026, 10, 26, 0, 0, 0, 123456000, time.UTC)}
	period := aiPeriodResponse(window, nil).(gin.H)
	require.Nil(t, period["id"].(*uuid.UUID))
	require.Equal(t, "2026-10-19T00:00:00.123456Z", period["starts_at"])
	item := aiUserQuotaResponse(domain.AIEmployeeQuota{Revision: revision, Weekly: domain.AIQuotaWindow{State: "unconfigured"}, FiveHour: domain.AIQuotaWindow{State: "unconfigured"}}, revision, time.Now())
	require.Nil(t, item["weekly_quota_microcredits"])
	require.Equal(t, "9007199254740993", item["company_revision"])
}

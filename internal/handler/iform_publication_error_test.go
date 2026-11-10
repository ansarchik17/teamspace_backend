package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func TestIFormPublicationIssuesAreLocalizedWithoutForwardingUpstreamText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := `{"code":"VALIDATION_ERROR","message":"SQLSTATE private token=secret","validation":{"errors":["private diagnostic"]},"publication_issues":[{"code":"CONNECTION_REQUIRED","message":"secret"},{"code":"TEMPLATE_REQUIRED"},{"code":"CONNECTION_REQUIRED"},{"code":"SETUP_CONFIRMATION_REQUIRED"}]}`
	outputs := map[string]string{}
	for _, locale := range []string{"ru", "kk"} {
		router := gin.New()
		router.Use(middleware.Localization(nil))
		router.POST("/", func(c *gin.Context) {
			respondIFormUpstreamError(c, 422, strings.NewReader(input), "/api/v1/processes/test/publish")
		})
		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("Accept-Language", locale)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 422 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "SQLSTATE") {
			t.Fatalf("unsafe publication response: %s", w.Body.String())
		}
		var result struct {
			Code       string `json:"code"`
			Validation struct {
				Valid      bool     `json:"valid"`
				Errors     []string `json:"errors"`
				Unresolved []string `json:"unresolvedItems"`
			} `json:"validation"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Code != "IFORM_PUBLICATION_BLOCKED" || result.Validation.Valid || len(result.Validation.Errors) != 2 || len(result.Validation.Unresolved) != 1 {
			t.Fatalf("bad validation contract: %s", w.Body.String())
		}
		outputs[locale] = w.Body.String()
	}
	if outputs["ru"] == outputs["kk"] {
		t.Fatal("publication issues were not localized")
	}
}

func TestIFormPublicationBoundaryRejectsUnknownMalformedAndUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"unknown code", "/api/v1/processes/test/publish", `{"code":"VALIDATION_ERROR","publication_issues":[{"code":"secret"}]}`, 422},
		{"wrong code type", "/api/v1/processes/test/publish", `{"code":{},"publication_issues":[{"code":"CONNECTION_REQUIRED"}]}`, 422},
		{"malformed item", "/api/v1/processes/test/publish", `{"code":"VALIDATION_ERROR","publication_issues":["secret"]}`, 422},
		{"legacy server", "/api/v1/processes/test/publish", `{"code":"VALIDATION_ERROR","message":"secret","validation":{"errors":["secret"]}}`, 422},
		{"other endpoint", "/api/v1/connections", `{"code":"VALIDATION_ERROR","publication_issues":[{"code":"CONNECTION_REQUIRED"}]}`, 422},
		{"server error", "/api/v1/processes/test/publish", `{"code":"VALIDATION_ERROR","publication_issues":[{"code":"CONNECTION_REQUIRED"}]}`, 500},
		{"invalid JSON", "/api/v1/processes/test/publish", `not json secret`, 422},
		{"too many issues", "/api/v1/processes/test/publish", `{"code":"VALIDATION_ERROR","publication_issues":[` + strings.Repeat(`{"code":"CONNECTION_REQUIRED"},`, 20) + `{"code":"CONNECTION_REQUIRED"}]}`, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", nil)
			respondIFormUpstreamError(c, tc.status, strings.NewReader(tc.body), tc.path)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), `"validation"`) {
				t.Fatalf("unsafe fallback: %s", w.Body.String())
			}
		})
	}
}

func TestIFormOnboardingValidationKeepsDomainReasonsAndDropsPrivateDetails(t *testing.T) {
	for _, locale := range []string{"ru", "kk"} {
		for _, tc := range []struct{ code, path string }{
			{"IIN_INVALID", "/api/v1/requests/request-1/submit"},
			{"IIN_INVALID", "/api/v1/requests/request-1/corrections"},
			{"ONBOARDING_CREDENTIALS_REQUIRED", "/api/v1/tasks/task-1/actions"},
			{"DATE_ORDER_INVALID", "/api/v1/requests/request-1/submit"},
			{"DATE_ORDER_INVALID", "/api/v1/requests/request-1/corrections"},
			{"DATE_INVALID", "/api/v1/requests/request-1/submit"},
			{"DATE_RANGE_REQUIRED", "/api/v1/requests/request-1/submit"},
			{"DELEGATION_PERIOD_INVALID", "/api/v1/approval-delegations"},
			{"DELEGATION_APPROVER_REQUIRED", "/api/v1/approval-delegations"},
			{"DELEGATION_OVERLAP", "/api/v1/approval-delegations"},
		} {
			router := gin.New()
			router.Use(middleware.Localization(nil))
			router.POST("/", func(c *gin.Context) {
				respondIFormUpstreamError(c, 422, strings.NewReader(`{"code":"`+tc.code+`","message":"private token=secret","fieldErrors":["secret"]}`), tc.path)
			})
			req := httptest.NewRequest("POST", "/", nil)
			req.Header.Set("Accept-Language", locale)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			var body map[string]any
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 422 || body["code"] != tc.code || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "fieldErrors") {
				t.Fatalf("unsafe domain error: %s", w.Body.String())
			}
			message, _ := body["message"].(string)
			if message == "" || strings.Contains(message, "error.") || strings.Contains(message, "iform.validation.") || message == "Проверьте введённые данные" {
				t.Fatalf("missing localized reason: %s", w.Body.String())
			}
		}
	}
	for _, tc := range []struct{ code, path string }{
		{"IIN_INVALID", "/api/v1/tasks/task-1/actions"},
		{"ONBOARDING_CREDENTIALS_REQUIRED", "/api/v1/requests/request-1/submit"},
		{"UNKNOWN_PRIVATE", "/api/v1/requests/request-1/submit"},
		{"DATE_ORDER_INVALID", "/api/v1/tasks/task-1/actions"},
		{"DELEGATION_OVERLAP", "/api/v1/requests/request-1/submit"},
		{"UNKNOWN_PRIVATE", "/api/v1/approval-delegations"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)
		respondIFormUpstreamError(c, 422, strings.NewReader(`{"code":"`+tc.code+`","message":"secret"}`), tc.path)
		if strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unrelated code escaped: %s", w.Body.String())
		}
	}
}

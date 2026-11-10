package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

var iformPublicationPath = regexp.MustCompile(`^/api/v1/processes/[A-Za-z0-9_-]+/publish$`)
var iformRequestValidationPath = regexp.MustCompile(`^/api/v1/requests/[A-Za-z0-9_-]+/(submit|corrections|draft)$`)
var iformDelegationPath = regexp.MustCompile(`^/api/v1/approval-delegations(?:/[A-Za-z0-9_-]+/revoke)?$`)
var iformTaskActionPath = regexp.MustCompile(`^/api/v1/tasks/[A-Za-z0-9_-]+/actions$`)

// Keep general upstream sanitization. Only bounded publication issues and
// endpoint-specific onboarding reasons become UI validation, never raw text.
func respondIFormUpstreamError(c *gin.Context, status int, body io.Reader, path string) {
	if status != http.StatusUnprocessableEntity || !(iformPublicationPath.MatchString(path) || iformRequestValidationPath.MatchString(path) || iformTaskActionPath.MatchString(path) || iformDelegationPath.MatchString(path)) {
		respondUpstreamError(c, status, body)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&payload); err != nil {
		respondJSON(c, status, nil)
		return
	}
	code, _ := payload["code"].(string)
	if (iformRequestValidationPath.MatchString(path) && (code == "DATE_INVALID" || code == "DATE_ORDER_INVALID" || code == "DATE_RANGE_REQUIRED")) || (iformDelegationPath.MatchString(path) && (code == "DATE_INVALID" || code == "DELEGATION_PERIOD_INVALID" || code == "DELEGATION_APPROVER_REQUIRED" || code == "DELEGATION_OVERLAP")) {
		middleware.RespondIFormValidation(c, code)
		return
	}
	if code == "IIN_INVALID" && iformRequestValidationPath.MatchString(path) || code == "ONBOARDING_CREDENTIALS_REQUIRED" && iformTaskActionPath.MatchString(path) {
		// Only these endpoint-specific domain codes cross the boundary. Generate
		// text locally; never forward an upstream message, field value or trace.
		middleware.RespondIFormValidation(c, code)
		return
	}
	if !iformPublicationPath.MatchString(path) {
		respondJSON(c, status, nil)
		return
	}
	issues, ok := payload["publication_issues"].([]any)
	if payload["code"] != "VALIDATION_ERROR" || !ok || len(issues) == 0 || len(issues) > 20 {
		respondJSON(c, status, payload)
		return
	}
	codes := make([]string, 0, len(issues))
	for _, raw := range issues {
		item, ok := raw.(map[string]any)
		if !ok {
			respondJSON(c, status, payload)
			return
		}
		code, _ := item["code"].(string)
		codes = append(codes, code)
	}
	if !middleware.RespondIFormPublicationIssues(c, codes) {
		respondJSON(c, status, payload)
	}
}

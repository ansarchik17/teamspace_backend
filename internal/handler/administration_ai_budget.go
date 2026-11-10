package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// Register inside the existing authenticated/localized administration groups.
// The company-budget PUT deliberately exists only in platform scope.
func (h *OrganizationHandler) RegisterAIBudgetRoutes(group *gin.RouterGroup, platform bool) {
	group.GET("/ai-budget", h.AdministrationAIBudget)
	group.GET("/ai-budget/users", h.AdministrationAIUsers)
	group.GET("/ai-budget/users/:user_id", h.AdministrationAIUser)
	group.PUT("/ai-budget/users/:user_id", h.SetAdministrationAIUserQuota)
	if platform {
		group.PUT("/ai-budget", h.SetAdministrationAIBudget)
	}
}

func aiAdministrationScope(c *gin.Context) (int64, *uuid.UUID, error) {
	actor, ok := middleware.GetUserID(c)
	if !ok || actor <= 0 {
		return 0, nil, apperrors.Unauthorized("authentication required")
	}
	org, err := accessOrganizationID(c)
	return actor, org, err
}

func (h *OrganizationHandler) AdministrationAIBudget(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, org, err := aiAdministrationScope(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.AdministrationAIBudget(c.Request.Context(), actor, org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, aiBudgetResponse(out))
}

func (h *OrganizationHandler) AdministrationAIUsers(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, org, err := aiAdministrationScope(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	filter, err := aiQuotaFilter(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.AdministrationAIUsers(c.Request.Context(), actor, org, filter)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	items := make([]any, 0, len(out.Items))
	for _, item := range out.Items {
		items = append(items, aiUserQuotaResponse(item, out.CompanyRevision, out.ServerTime))
	}
	respondJSON(c, http.StatusOK, gin.H{"items": items, "total": out.Total, "limit": out.Limit, "offset": out.Offset, "server_time": aiTime(out.ServerTime), "company_revision": aiAmount(out.CompanyRevision), "period": aiPeriodResponse(out.Window, out.PeriodID)})
}

func (h *OrganizationHandler) AdministrationAIUser(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, org, err := aiAdministrationScope(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	target, err := aiTarget(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.AdministrationAIUser(c.Request.Context(), actor, target, org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, aiUserQuotaResponse(out.Item, out.CompanyRevision, out.ServerTime))
}

func (h *OrganizationHandler) SetAdministrationAIBudget(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, org, err := aiAdministrationScope(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	if org == nil {
		h.respondError(c, apperrors.Forbidden("platform administrator required"))
		return
	}
	values, err := aiSettingsBody(c, []string{"weekly_budget_microcredits", "max_five_hour_microcredits", "revision"}, "service_weekly_quota_microcredits")
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid AI budget JSON"))
		return
	}
	req := domain.SetAIBudgetRequest{WeeklyBudget: aiValue(values["weekly_budget_microcredits"]), MaxFiveHour: aiValue(values["max_five_hour_microcredits"]), ServiceQuota: aiValue(values["service_weekly_quota_microcredits"]), Revision: aiValue(values["revision"])}
	if !req.Valid() {
		h.respondError(c, apperrors.Validation("invalid AI budget"))
		return
	}
	err = h.service.SetAdministrationAIBudget(c.Request.Context(), actor, org, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *OrganizationHandler) SetAdministrationAIUserQuota(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, org, err := aiAdministrationScope(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	target, err := aiTarget(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	values, err := aiSettingsBody(c, []string{"weekly_quota_microcredits", "five_hour_limit_microcredits", "revision", "company_revision"})
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid AI quota JSON"))
		return
	}
	req := domain.SetAIUserQuotaRequest{WeeklyQuota: aiValue(values["weekly_quota_microcredits"]), FiveHourLimit: aiValue(values["five_hour_limit_microcredits"]), Revision: aiValue(values["revision"]), CompanyRevision: aiValue(values["company_revision"])}
	if !req.Valid() {
		h.respondError(c, apperrors.Validation("invalid AI quota"))
		return
	}
	err = h.service.SetAdministrationAIUserQuota(c.Request.Context(), actor, target, org, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func aiValue(value int64) *int64 { return &value }

func (h *OrganizationHandler) PlatformPersonalAIQuota(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, ok := middleware.GetUserID(c)
	if !ok {
		h.respondError(c, apperrors.Unauthorized("authentication required"))
		return
	}
	target, err := aiTarget(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	var req *domain.SetAIUserQuotaRequest
	if c.Request.Method == http.MethodPut {
		values, e := aiSettingsBody(c, []string{"weekly_quota_microcredits", "five_hour_limit_microcredits", "revision"})
		if e != nil {
			h.respondError(c, apperrors.Validation("invalid AI quota JSON"))
			return
		}
		req = &domain.SetAIUserQuotaRequest{WeeklyQuota: aiValue(values["weekly_quota_microcredits"]), FiveHourLimit: aiValue(values["five_hour_limit_microcredits"]), Revision: aiValue(values["revision"]), CompanyRevision: aiValue(0)}
	}
	out, err := h.service.PlatformPersonalAIQuota(c.Request.Context(), actor, target, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"user_id": out["user_id"], "weekly_quota_microcredits": aiAmount(out["weekly_quota_microcredits"].(int64)), "five_hour_limit_microcredits": aiAmount(out["five_hour_limit_microcredits"].(int64)), "revision": aiAmount(out["revision"].(int64))})
}

func aiTarget(c *gin.Context) (int64, error) {
	raw := c.Param("user_id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, apperrors.Validation("invalid user id")
	}
	return id, nil
}

func aiQuotaFilter(c *gin.Context) (domain.AdministrationUserFilter, error) {
	filter := domain.AdministrationUserFilter{Limit: 25}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return filter, apperrors.Validation("invalid pagination")
	}
	for _, field := range []string{"limit", "offset", "q"} {
		if len(query[field]) > 1 {
			return filter, apperrors.Validation("duplicate pagination field")
		}
	}
	if raw, ok := query["limit"]; ok {
		filter.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			return filter, apperrors.Validation("invalid pagination")
		}
	}
	if raw, ok := query["offset"]; ok {
		filter.Offset, err = strconv.Atoi(raw[0])
		if err != nil {
			return filter, apperrors.Validation("invalid pagination")
		}
	}
	filter.Query = strings.TrimSpace(query.Get("q"))
	if !utf8.ValidString(filter.Query) || utf8.RuneCountInString(filter.Query) > 255 {
		return filter, apperrors.Validation("invalid search query")
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || int64(filter.Offset) > 2147483647 {
		return filter, apperrors.Validation("invalid pagination")
	}
	return filter, nil
}

// Token decoding detects duplicate keys too (DisallowUnknownFields alone does
// not). No numeric JSON, null, unknown fields or trailing JSON are accepted.
func aiSettingsBody(c *gin.Context, fields []string, optional ...string) (map[string]int64, error) {
	invalid := errors.New("invalid AI settings JSON")
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, invalid
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	d := json.NewDecoder(c.Request.Body)
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	for _, field := range optional {
		allowed[field] = true
	}
	values := map[string]int64{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, invalid
		}
		field, ok := token.(string)
		if !ok || !allowed[field] {
			return nil, invalid
		}
		if _, exists := values[field]; exists {
			return nil, invalid
		}
		var raw string
		if err = d.Decode(&raw); err != nil {
			return nil, invalid
		}
		value, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || value < 0 || strconv.FormatInt(value, 10) != raw {
			return nil, invalid
		}
		values[field] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, invalid
	}
	for _, field := range fields {
		if _, exists := values[field]; !exists {
			return nil, invalid
		}
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, invalid
	}
	return values, nil
}

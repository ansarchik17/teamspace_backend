package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

var iformPath = regexp.MustCompile(`^(me|tenants|directory|processes|forms|requests|tasks|documents|files|materials|chat|proposals|notifications|audit-events|incidents|templates|calendars|connector-types|connections|approval-delegations|employee-chat|employee-chats|demo)(/[A-Za-z0-9_-]+)*$`)

func iformAPIPath(path string) (string, bool) {
	path = strings.TrimPrefix(path, "/")
	return "/api/v1/" + path, iformPath.MatchString(path)
}

func iformAPIBase() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("IFORM_API_URL")), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", apperrors.Conflict("Сервер iForm ещё не настроен")
	}
	return base, nil
}

// The bridge never forwards the browser's token, cookies, tenant, host or role headers.
func (h *OrganizationHandler) ProxyIFormAPI(c *gin.Context) {
	path, ok := iformAPIPath(c.Param("path"))
	if !ok {
		respondJSON(c, 400, gin.H{"message": apiMessage(c, "Недопустимый путь iForm")})
		return
	}
	switch c.Request.Method {
	case "GET", "POST", "PATCH", "DELETE":
	default:
		c.Status(405)
		return
	}
	actor := middleware.MustGetUserID(c)
	context, err := h.service.IFormIdentity(c.Request.Context(), actor)
	if err != nil {
		failure := organizationAppError(err)
		if failure.StatusCode == http.StatusForbidden {
			respondJSON(c, http.StatusForbidden, gin.H{"code": "iform_access_denied", "message": failure.Message})
		} else {
			h.respondError(c, failure)
		}
		return
	}
	if expected := c.GetHeader("X-IForm-Expected-Tenant"); expected != "" && expected != context.OrganizationID.String() {
		c.Header("X-IForm-Tenant", context.OrganizationID.String())
		respondJSON(c, http.StatusConflict, gin.H{"code": "iform_tenant_changed", "message": apiMessage(c, "Компания пользователя изменилась. Обновите iForm")})
		return
	}
	base, err := iformAPIBase()
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	token, err := service.SignIFormRequest(context, c.Request.Method, path)
	if err != nil {
		respondJSON(c, 503, gin.H{"message": apiMessage(c, "Защищённое подключение iForm ещё не настроено")})
		return
	}
	body := c.Request.Body
	if body == nil {
		body = http.NoBody
	}
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, body, 22<<20))
	if err != nil {
		respondJSON(c, 413, gin.H{"message": apiMessage(c, "Превышен размер запроса iForm")})
		return
	}
	endpoint := base + path
	if c.Request.URL.RawQuery != "" {
		endpoint += "?" + c.Request.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, endpoint, bytes.NewReader(data))
	if err != nil {
		respondJSON(c, 502, gin.H{"code": "iform_unavailable"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for _, header := range []string{"Content-Type", "Idempotency-Key", "Accept"} {
		if value := c.GetHeader(header); value != "" {
			req.Header.Set(header, value)
		}
	}
	client := &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		respondJSON(c, 502, gin.H{"message": apiMessage(c, "Сервер iForm недоступен")})
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		respondJSON(c, 502, gin.H{"message": apiMessage(c, "Некорректный ответ сервера iForm")})
		return
	}
	c.Header("Cache-Control", "no-store, private")
	if response.StatusCode >= 400 {
		respondIFormUpstreamError(c, response.StatusCode, response.Body, path)
		return
	}
	for _, header := range []string{"Content-Type", "Content-Disposition", "Retry-After", "X-Content-Type-Options", "X-IForm-Tenant"} {
		if value := response.Header.Get(header); value != "" {
			c.Header(header, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

// Internal service credential plus an expected tenant. Neither browser input nor
// a platform administrator can use this endpoint to select a foreign company.
func (h *OrganizationHandler) IFormContext(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Subject        string    `json:"subject"`
		OrganizationID uuid.UUID `json:"organization_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OrganizationID == uuid.Nil || !strings.HasPrefix(req.Subject, "workspace:") {
		respondJSON(c, 400, gin.H{"message": apiMessage(c, "Некорректный контекст iForm")})
		return
	}
	actor, err := strconv.ParseInt(strings.TrimPrefix(req.Subject, "workspace:"), 10, 64)
	if err != nil || actor <= 0 || req.Subject != serviceSubject(actor) {
		c.Status(400)
		return
	}
	context, err := h.service.IFormContext(c.Request.Context(), actor, req.OrganizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store, private")
	respondJSON(c, 200, context)
}

func serviceSubject(actor int64) string { return "workspace:" + strconv.FormatInt(actor, 10) }

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

var growyTrainingReadPath = regexp.MustCompile(`^users/enrollments/([0-9]+/progress-detail/)?$|^training-programs/my-unfinished/$`)

var growyAPIPath = regexp.MustCompile(`^(crm|whatsapp)/[a-z0-9_/-]+(\.(csv|xlsx))?$|^integrations/meta/[a-z0-9_/-]+$|^context/$`)

// Growy's LMS and CRM use different API roots. Keep the LMS read allowlist
// separate so exposing personal training data does not enable training writes.
func growyAPIUpstreamPath(method, path string) (string, bool) {
	if strings.Contains(path, "//") {
		return "", false
	}
	if growyAPIPath.MatchString(path) {
		return "/api/platform/v1/" + path, true
	}
	if method != http.MethodGet || !growyTrainingReadPath.MatchString(path) {
		return "", false
	}
	if strings.HasPrefix(path, "users/enrollments/") {
		return "/api/my/" + path, true
	}
	return "/api/" + path, true
}

func growyHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func growyPublicBase() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GROWY_PUBLIC_URL")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")), "/") + "/growy"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", &url.Error{Op: "parse", URL: "Growy public URL", Err: io.ErrUnexpectedEOF}
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return "", &url.Error{Op: "parse", URL: "Growy public URL", Err: io.ErrUnexpectedEOF}
	}
	return base, nil
}

// The route allowlist determines the backend. Clients cannot choose an origin.
type growyAPIConnection struct {
	base string
	lms  bool
}

func growyConnectionForPath(upstreamPath string) (growyAPIConnection, error) {
	lms := !strings.HasPrefix(upstreamPath, "/api/platform/v1/")
	var base string
	var err error
	if lms {
		base, err = service.GrowyLMSPublicBase()
	} else {
		base, err = growyPublicBase()
	}
	return growyAPIConnection{base: base, lms: lms}, err
}

func (connection growyAPIConnection) request(ctx context.Context, method, path, slug string, body io.Reader) (*http.Request, error) {
	if connection.lms {
		return service.NewGrowyLMSAPIRequest(ctx, method, connection.base, path, slug, body)
	}
	return service.NewGrowyAPIRequest(ctx, method, connection.base, path, body)
}

// ProxyGrowyAPI lets Workspace UI use CRM and WhatsApp in the authenticated
// employee's organization. No administrator credential or client tenant ID is
// forwarded. Current membership is validated on every call; short-lived upstream tokens
// are reused only for the same server-resolved identity and organization.
func (h *OrganizationHandler) ProxyGrowyAPI(c *gin.Context) {
	startedAt := time.Now()
	path := strings.TrimPrefix(c.Param("path"), "/")
	upstreamPath, allowed := growyAPIUpstreamPath(c.Request.Method, path)
	if !allowed {
		respondJSON(c, http.StatusBadRequest, gin.H{"code": "invalid_growy_api_path"})
		return
	}
	switch c.Request.Method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
	default:
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	claims, ok := middleware.GetClaims(c)
	if !ok || claims.UserID <= 0 || strings.TrimSpace(claims.Email) == "" {
		respondJSON(c, http.StatusUnauthorized, gin.H{"code": "workspace_identity_unavailable"})
		return
	}
	connection, err := growyConnectionForPath(upstreamPath)
	if err != nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"code": "growy_not_configured"})
		return
	}
	var details *domain.GrowyHandoffDetails
	if connection.lms {
		details, err = h.service.GrowyHandoffDetails(c.Request.Context(), claims.UserID)
	} else {
		details, err = h.service.GrowyCRMHandoffDetails(c.Request.Context(), claims.UserID)
	}
	if err != nil {
		respondJSON(c, http.StatusForbidden, gin.H{"code": "growy_company_access_unavailable"})
		return
	}
	// The fingerprint changes on tenant/role/profile/configuration changes. Never
	// cache the local membership check above or an upstream API response.
	key := growySessionKey(fmt.Sprintf("lms=%t:%s", connection.lms, connection.base), claims, details)
	membershipDuration := time.Since(startedAt)
	sessionStarted := time.Now()
	access, cacheHit, sessionErr := h.growySessions.get(c.Request.Context(), key, func() (string, time.Time, error) {
		started := time.Now()
		if connection.lms {
			err = h.service.ProvisionGrowyLMSTenant(c.Request.Context(), details.OrganizationID, details.OrganizationName, details.OrganizationSlug, connection.base)
		} else {
			err = h.service.ProvisionGrowyTenant(c.Request.Context(), details.OrganizationID, details.OrganizationName, details.OrganizationSlug)
		}
		if err != nil {
			h.logger.Warn("Growy API tenant provisioning failed", zap.Error(err), zap.String("organization_id", details.OrganizationID.String()))
			return "", time.Time{}, &growySessionError{http.StatusServiceUnavailable, "growy_provisioning_failed"}
		}
		ticket, err := service.SignGrowyUserTicket(claims.UserID, claims.Email, claims.Username, claims.FirstName, claims.LastName, details)
		if err != nil {
			return "", time.Time{}, &growySessionError{http.StatusServiceUnavailable, "growy_sso_not_configured"}
		}
		body, _ := json.Marshal(map[string]string{"ticket": ticket})
		request, err := connection.request(c.Request.Context(), http.MethodPost, "/api/token/workspace/", details.OrganizationSlug, bytes.NewReader(body))
		if err != nil {
			return "", time.Time{}, &growySessionError{http.StatusBadGateway, "growy_exchange_unavailable"}
		}
		request.Header.Set("Content-Type", "application/json")
		client := growyHTTPClient()
		response, err := client.Do(request)
		if err != nil {
			return "", time.Time{}, &growySessionError{http.StatusBadGateway, "growy_exchange_unavailable"}
		}
		var session struct {
			Access    string `json:"access"`
			ExpiresIn int    `json:"expires_in"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&session)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || session.Access == "" {
			// Do not relay tokens, authentication error bodies or private upstream data.
			status := http.StatusBadGateway
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusConflict {
				status = response.StatusCode
			}
			return "", time.Time{}, &growySessionError{status, "growy_exchange_failed"}
		}

		return session.Access, growySessionExpiry(session.Access, session.ExpiresIn, started), nil
	})
	sessionDuration := time.Since(sessionStarted)
	if sessionErr != nil {
		failure, ok := sessionErr.(*growySessionError)
		if !ok {
			failure = &growySessionError{http.StatusBadGateway, "growy_exchange_unavailable"}
		}
		respondJSON(c, failure.status, gin.H{"code": failure.code})
		return
	}
	var input io.Reader
	if c.Request.Body != nil {
		data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024*1024))
		if err != nil {
			respondJSON(c, http.StatusRequestEntityTooLarge, gin.H{"code": "growy_payload_too_large"})
			return
		}
		input = bytes.NewReader(data)
	}
	endpoint := upstreamPath
	if c.Request.URL.RawQuery != "" {
		endpoint += "?" + c.Request.URL.RawQuery
	}
	request, err := connection.request(c.Request.Context(), c.Request.Method, endpoint, details.OrganizationSlug, input)
	if err != nil {
		respondJSON(c, http.StatusBadGateway, gin.H{"code": "growy_api_unavailable"})
		return
	}
	request.Header.Set("Authorization", "Bearer "+access)
	request.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	timeoutSeconds, parseErr := strconv.Atoi(strings.TrimSpace(os.Getenv("GROWY_API_TIMEOUT_SECONDS")))
	if parseErr != nil || timeoutSeconds < 15 || timeoutSeconds > 120 {
		timeoutSeconds = 60
	}
	apiClient := growyHTTPClient()
	apiClient.Timeout = time.Duration(timeoutSeconds) * time.Second
	upstreamStarted := time.Now()
	response, err := apiClient.Do(request)
	upstreamDuration := time.Since(upstreamStarted)
	if err != nil {
		respondJSON(c, http.StatusBadGateway, gin.H{"code": "growy_api_unavailable"})
		return
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		h.growySessions.invalidate(key, access)
	}
	c.Header("Server-Timing", fmt.Sprintf("workspace_access;dur=%.1f, growy_session;dur=%.1f;desc=\"%s\", growy_api;dur=%.1f", float64(membershipDuration.Microseconds())/1000, float64(sessionDuration.Microseconds())/1000, map[bool]string{true: "reused", false: "exchanged"}[cacheHit], float64(upstreamDuration.Microseconds())/1000))
	defer response.Body.Close()
	c.Header("Cache-Control", "no-store, private")
	if response.StatusCode >= 400 {
		respondUpstreamError(c, response.StatusCode, response.Body)
		return
	}
	// Fixed upstream and validated paths; no incoming cookies/auth/host headers.
	for _, header := range []string{"Content-Type", "Content-Disposition", "Retry-After", "X-Content-Type-Options"} {
		if value := response.Header.Get(header); value != "" {
			c.Header(header, value)
		}
	}
	c.Status(response.StatusCode)
	_, copyErr := io.Copy(c.Writer, response.Body)
	if time.Since(startedAt) > 2*time.Second || copyErr != nil {
		h.logger.Warn("Slow Growy API request", zap.String("method", c.Request.Method), zap.String("route", growyTimingRoute(path)), zap.Int("upstream_status", response.StatusCode), zap.Bool("session_reused", cacheHit), zap.Duration("workspace_access", membershipDuration), zap.Duration("session", sessionDuration), zap.Duration("upstream_headers", upstreamDuration), zap.Duration("total", time.Since(startedAt)), zap.Bool("stream_failed", copyErr != nil))
	}
}

// Log route shape, never query values, tokens, message bodies or account IDs.
func growyTimingRoute(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if _, err := strconv.ParseUint(part, 10, 64); err == nil {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

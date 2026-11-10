package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	defaultBaseURL     = "https://idp.egov.kz"
	authorizePath      = "/idp/oauth/authorize"
	tokenPath          = "/idp/oauth/token"
	userBasicPath      = "/idp/resource/user/basic"
	userPhonePath      = "/idp/resource/user/phone"
	userEmailPath      = "/idp/resource/user/email"
	formContentType    = "application/x-www-form-urlencoded; charset=utf-8"
	jsonContentType    = "application/json"
	defaultHTTPTimeout = 15 * time.Second
)

type Client struct {
	baseURL      string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	logger       *zap.Logger
}

type tokenExchangeOptions struct {
	includeClientIDInBody     bool
	includeClientSecretInBody bool
	useBasicAuth              bool
	attempt                   string
}

type APIError struct {
	StatusCode       int
	ErrorCode        string
	ErrorDescription string
	RequiredScope    string
	RawBody          string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrorCode != "" || e.ErrorDescription != "" {
		return fmt.Sprintf("idp api error: status=%d error=%s description=%s", e.StatusCode, e.ErrorCode, e.ErrorDescription)
	}
	return fmt.Sprintf("idp api error: status=%d body=%s", e.StatusCode, e.RawBody)
}

type errorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Scope            string `json:"scope"`
	Message          string `json:"message"`
}

func NewClient(baseURL, clientID, clientSecret string, timeout time.Duration, logger ...*zap.Logger) *Client {
	baseURL = normalizeIDPBaseURL(baseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	log := zap.NewNop()
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}

	return &Client{
		baseURL:      baseURL,
		clientID:     strings.TrimSpace(clientID),
		clientSecret: strings.TrimSpace(clientSecret),
		httpClient:   &http.Client{Timeout: timeout},
		logger:       log.With(zap.String("component", "idp_client")),
	}
}

func normalizeIDPBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return ""
	}
	baseURL = strings.TrimSuffix(baseURL, "/idp")
	return baseURL
}

func (c *Client) AuthorizeURL(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
	if strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("client id is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return "", fmt.Errorf("redirect uri is required")
	}
	if strings.TrimSpace(state) == "" {
		return "", fmt.Errorf("state is required")
	}
	if len(scopes) == 0 {
		return "", fmt.Errorf("at least one scope is required")
	}

	u, err := url.Parse(c.baseURL + authorizePath)
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("scope", strings.Join(scopes, " "))
	if strings.TrimSpace(lang) != "" {
		q.Set("lang", lang)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", strings.TrimSpace(code))
	values.Set("redirect_uri", strings.TrimSpace(redirectURI))

	token, err := c.exchangeToken(ctx, values, tokenExchangeOptions{
		includeClientIDInBody: false,
		useBasicAuth:          true,
		attempt:               "basic_without_client_id_body",
	})
	if shouldRetryTokenExchangeWithClientID(err) {
		token, retryErr := c.exchangeToken(ctx, values, tokenExchangeOptions{
			includeClientIDInBody: true,
			useBasicAuth:          true,
			attempt:               "basic_with_client_id_body_retry",
		})
		if shouldRetryTokenExchangeWithClientSecretPost(retryErr) {
			return c.exchangeToken(ctx, values, tokenExchangeOptions{
				includeClientIDInBody:     true,
				includeClientSecretInBody: true,
				useBasicAuth:              false,
				attempt:                   "client_secret_post_retry",
			})
		}
		return token, retryErr
	}
	return token, err
}

func (c *Client) RefreshAccessToken(ctx context.Context, refreshToken string) (*domain.IDPTokenResponse, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(refreshToken))
	return c.exchangeToken(ctx, values, tokenExchangeOptions{
		includeClientIDInBody: false,
		useBasicAuth:          true,
		attempt:               "basic_without_client_id_body",
	})
}

func (c *Client) GetUserBasic(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error) {
	var result domain.IDPUserBasicResponse
	if err := c.getResource(ctx, userBasicPath, accessToken, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetUserPhone(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error) {
	var result domain.IDPUserPhoneResponse
	if err := c.getResource(ctx, userPhonePath, accessToken, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetUserEmail(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error) {
	var result domain.IDPUserEmailResponse
	if err := c.getResource(ctx, userEmailPath, accessToken, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) exchangeToken(ctx context.Context, values url.Values, opts tokenExchangeOptions) (*domain.IDPTokenResponse, error) {
	payload := cloneURLValues(values)
	if opts.includeClientIDInBody {
		payload.Set("client_id", c.clientID)
	}
	if opts.includeClientSecretInBody {
		payload.Set("client_secret", c.clientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+tokenPath, bytes.NewBufferString(payload.Encode()))
	if err != nil {
		return nil, err
	}
	if opts.useBasicAuth {
		req.SetBasicAuth(c.clientID, c.clientSecret)
	}
	req.Header.Set("Content-Type", formContentType)
	req.Header.Set("Accept", jsonContentType)

	c.logTokenRequest(req, payload, opts)
	var result domain.IDPTokenResponse
	if err := c.do(req, &result); err != nil {
		c.logTokenResult(req, payload, opts, err)
		return nil, err
	}
	c.logTokenSuccess(req, payload, opts)
	return &result, nil
}

func (c *Client) getResource(ctx context.Context, path, accessToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", jsonContentType)
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logRequestError(req, err)
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return parseAPIError(resp.StatusCode, body)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode idp response: %w", err)
	}
	return nil
}

func (c *Client) logRequestError(req *http.Request, err error) {
	if c == nil || c.logger == nil || req == nil || req.URL == nil {
		return
	}

	networkClass := classifyNetworkError(err)
	fields := []zap.Field{
		zap.String("request_method", req.Method),
		zap.String("request_url", req.URL.String()),
		zap.String("error_type", fmt.Sprintf("%T", err)),
		zap.String("error_message", err.Error()),
		zap.Bool("timeout", isTimeoutError(err)),
		zap.String("network_class", networkClass),
		zap.String("go_version", runtime.Version()),
	}
	c.logger.Warn("idp_http_request_failed", fields...)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func classifyNetworkError(err error) string {
	if err == nil {
		return "unknown"
	}
	lowered := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lowered, "deadline exceeded") || strings.Contains(lowered, "timeout"):
		return "timeout"
	case strings.Contains(lowered, "no such host") || strings.Contains(lowered, "temporary failure in name resolution"):
		return "dns"
	case strings.Contains(lowered, "connection refused"):
		return "connection_refused"
	case strings.Contains(lowered, "connection reset"):
		return "connection_reset"
	case strings.Contains(lowered, "eof"):
		return "eof"
	case strings.Contains(lowered, "tls:") || strings.Contains(lowered, "handshake"):
		return "tls"
	case strings.Contains(lowered, "protocol"):
		return "protocol"
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return "timeout"
		}
		if opErr.Op == "dial" {
			return "connection"
		}
	}
	return "network"
}

func parseAPIError(statusCode int, body []byte) error {
	apiErr := &APIError{
		StatusCode: statusCode,
		RawBody:    strings.TrimSpace(string(body)),
	}

	var parsed errorResponse
	if err := json.Unmarshal(body, &parsed); err == nil {
		apiErr.ErrorCode = strings.TrimSpace(parsed.Error)
		apiErr.ErrorDescription = strings.TrimSpace(parsed.ErrorDescription)
		apiErr.RequiredScope = strings.TrimSpace(parsed.Scope)
		if apiErr.ErrorDescription == "" {
			apiErr.ErrorDescription = strings.TrimSpace(parsed.Message)
		}
	}

	return apiErr
}

func shouldRetryTokenExchangeWithClientID(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		return false
	}

	combined := strings.ToLower(strings.TrimSpace(apiErr.ErrorCode + " " + apiErr.ErrorDescription + " " + apiErr.RawBody))
	return combined == "" ||
		strings.Contains(combined, "unauthorized") ||
		strings.Contains(combined, "invalid_client") ||
		strings.Contains(combined, "client")
}

func shouldRetryTokenExchangeWithClientSecretPost(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		return false
	}

	combined := strings.ToLower(strings.TrimSpace(apiErr.ErrorCode + " " + apiErr.ErrorDescription + " " + apiErr.RawBody))
	return combined == "" ||
		strings.Contains(combined, "unauthorized") ||
		strings.Contains(combined, "invalid_client") ||
		strings.Contains(combined, "client")
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}

func (c *Client) logTokenRequest(req *http.Request, values url.Values, opts tokenExchangeOptions) {
	if c == nil || c.logger == nil || req == nil || req.URL == nil {
		return
	}
	fields := []zap.Field{
		zap.String("request_method", req.Method),
		zap.String("request_url", req.URL.String()),
		zap.String("grant_type", firstValue(values, "grant_type")),
		zap.String("redirect_uri", firstValue(values, "redirect_uri")),
		zap.Bool("has_code", firstValue(values, "code") != ""),
		zap.Bool("has_refresh_token", firstValue(values, "refresh_token") != ""),
		zap.Bool("basic_auth_enabled", opts.useBasicAuth),
		zap.Bool("client_id_in_body", opts.includeClientIDInBody),
		zap.Bool("client_secret_in_body", opts.includeClientSecretInBody),
		zap.String("attempt", opts.attempt),
	}
	c.logger.Info("idp_token_request_started", fields...)
}

func (c *Client) logTokenSuccess(req *http.Request, values url.Values, opts tokenExchangeOptions) {
	if c == nil || c.logger == nil || req == nil || req.URL == nil {
		return
	}
	c.logger.Info("idp_token_request_succeeded",
		zap.String("request_method", req.Method),
		zap.String("request_url", req.URL.String()),
		zap.String("grant_type", firstValue(values, "grant_type")),
		zap.Bool("client_id_in_body", opts.includeClientIDInBody),
		zap.Bool("client_secret_in_body", opts.includeClientSecretInBody),
		zap.String("attempt", opts.attempt),
	)
}

func (c *Client) logTokenResult(req *http.Request, values url.Values, opts tokenExchangeOptions, err error) {
	if c == nil || c.logger == nil || req == nil || req.URL == nil || err == nil {
		return
	}
	fields := []zap.Field{
		zap.String("request_method", req.Method),
		zap.String("request_url", req.URL.String()),
		zap.String("grant_type", firstValue(values, "grant_type")),
		zap.Bool("client_id_in_body", opts.includeClientIDInBody),
		zap.Bool("client_secret_in_body", opts.includeClientSecretInBody),
		zap.String("attempt", opts.attempt),
		zap.String("error_type", fmt.Sprintf("%T", err)),
		zap.String("error_message", err.Error()),
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		fields = append(fields,
			zap.Int("status_code", apiErr.StatusCode),
			zap.String("idp_error", apiErr.ErrorCode),
			zap.String("idp_error_description", apiErr.ErrorDescription),
			zap.String("raw_body_preview", truncateLogValue(apiErr.RawBody, 256)),
		)
	}
	c.logger.Warn("idp_token_request_failed", fields...)
}

func firstValue(values url.Values, key string) string {
	if values == nil {
		return ""
	}
	return strings.TrimSpace(values.Get(key))
}

func truncateLogValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

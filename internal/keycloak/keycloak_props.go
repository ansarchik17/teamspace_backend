package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
}

type AuthCodeOptions struct {
	Scope               string
	Prompt              string
	LoginHint           string
	IDPHint             string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string
}

type RequestError struct {
	Operation        string `json:"operation"`
	StatusCode       int    `json:"status_code"`
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
	Body             string `json:"body,omitempty"`
}

func (e *RequestError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.ErrorCode) != "" {
		return fmt.Sprintf(
			"keycloak %s failed: status=%d error=%s error_description=%s",
			e.Operation,
			e.StatusCode,
			e.ErrorCode,
			e.ErrorDescription,
		)
	}
	return fmt.Sprintf("keycloak %s failed: status=%d body=%s", e.Operation, e.StatusCode, e.Body)
}

func (e *RequestError) IsInvalidGrant() bool {
	return e != nil && strings.EqualFold(strings.TrimSpace(e.ErrorCode), "invalid_grant")
}

func (e *RequestError) IsOfflineSessionNotFound() bool {
	return e.IsInvalidGrant() && strings.Contains(strings.ToLower(strings.TrimSpace(e.ErrorDescription)), "offline user session not found")
}

type Client struct {
	base       string
	publicBase string
	realm      string
	cid        string
	secret     string
	http       *http.Client
}

func New(base, realm, cid, secret string) *Client {
	return &Client{
		base: base, realm: realm, cid: cid, secret: secret,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func NewWithPublicURL(base, publicBase, realm, cid, secret string) *Client {
	client := New(base, realm, cid, secret)
	client.publicBase = publicBase
	return client
}

func (c *Client) tokenURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.base, c.realm)
}
func (c *Client) publicRealmURL() string {
	base := strings.TrimSpace(c.publicBase)
	if base == "" {
		base = c.base
	}
	return fmt.Sprintf("%s/realms/%s", strings.TrimRight(base, "/"), c.realm)
}
func (c *Client) AuthCodeURL(redirectURI, state string, loginHints ...string) string {
	opts := AuthCodeOptions{
		Scope: "openid email profile",
	}
	if len(loginHints) > 0 {
		opts.LoginHint = strings.TrimSpace(loginHints[0])
	}
	return c.authCodeURL(redirectURI, state, opts)
}

// AuthCodeURLWithIdentityProvider starts the broker flow through one specific
// identity provider selected by the server-side organization configuration.
func (c *Client) AuthCodeURLWithIdentityProvider(redirectURI, state, idpHint string, loginHints ...string) string {
	opts := AuthCodeOptions{Scope: "openid email profile", IDPHint: strings.TrimSpace(idpHint)}
	if len(loginHints) > 0 {
		opts.LoginHint = strings.TrimSpace(loginHints[0])
	}
	return c.authCodeURL(redirectURI, state, opts)
}

// AuthCodeURLSilent builds an authorization URL with prompt=none for silent SSO check.
// Keycloak will return a code if a valid session exists, or error=login_required otherwise.
func (c *Client) AuthCodeURLSilent(redirectURI, state string) string {
	return c.authCodeURL(redirectURI, state, AuthCodeOptions{
		Scope:  "openid email profile",
		Prompt: "none",
	})
}

func (c *Client) AuthCodeURLWithNonce(redirectURI, state, nonce string, loginHints ...string) string {
	opts := AuthCodeOptions{
		Scope: "openid email profile",
		Nonce: strings.TrimSpace(nonce),
	}
	if len(loginHints) > 0 {
		opts.LoginHint = strings.TrimSpace(loginHints[0])
	}
	return c.authCodeURL(redirectURI, state, opts)
}

func (c *Client) MobileAuthCodeURL(redirectURI, state, codeChallenge, codeChallengeMethod string) string {
	return c.authCodeURL(redirectURI, state, AuthCodeOptions{
		Scope:               "openid email profile offline_access",
		CodeChallenge:       strings.TrimSpace(codeChallenge),
		CodeChallengeMethod: strings.TrimSpace(codeChallengeMethod),
	})
}

func (c *Client) authCodeURL(redirectURI, state string, opts AuthCodeOptions) string {
	values := url.Values{}
	values.Set("client_id", c.cid)
	values.Set("redirect_uri", redirectURI)
	values.Set("response_type", "code")
	scope := strings.TrimSpace(opts.Scope)
	if scope == "" {
		scope = "openid email profile"
	}
	values.Set("scope", scope)
	if strings.TrimSpace(state) != "" {
		values.Set("state", state)
	}
	if prompt := strings.TrimSpace(opts.Prompt); prompt != "" {
		values.Set("prompt", prompt)
	}
	if loginHint := strings.TrimSpace(opts.LoginHint); loginHint != "" {
		values.Set("login_hint", loginHint)
	}
	if idpHint := strings.TrimSpace(opts.IDPHint); idpHint != "" {
		values.Set("kc_idp_hint", idpHint)
	}
	if nonce := strings.TrimSpace(opts.Nonce); nonce != "" {
		values.Set("nonce", nonce)
	}
	if challenge := strings.TrimSpace(opts.CodeChallenge); challenge != "" {
		values.Set("code_challenge", challenge)
	}
	if method := strings.TrimSpace(opts.CodeChallengeMethod); method != "" {
		values.Set("code_challenge_method", method)
	}
	return c.publicRealmURL() + "/protocol/openid-connect/auth?" + values.Encode()
}
func (c *Client) logoutURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/logout", c.base, c.realm)
}

// LogoutURL builds the Keycloak end-session URL for browser-redirect logout.
// Navigating the browser here clears KEYCLOAK_IDENTITY and KEYCLOAK_SESSION cookies.
func (c *Client) LogoutURL(postLogoutRedirectURI string) string {
	values := url.Values{}
	values.Set("client_id", c.cid)
	if strings.TrimSpace(postLogoutRedirectURI) != "" {
		values.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	return c.publicRealmURL() + "/protocol/openid-connect/logout?" + values.Encode()
}

func (c *Client) Password(ctx context.Context, username, password string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", c.cid)
	if c.secret != "" {
		form.Set("client_secret", c.secret)
	}
	form.Set("username", username)
	form.Set("password", password)
	form.Set("scope", "openid profile email offline_access")
	req, _ := http.NewRequestWithContext(ctx, "POST", c.tokenURL(), bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, parseRequestError("password", resp)
	}

	var out TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.cid)
	if c.secret != "" {
		form.Set("client_secret", c.secret)
	}
	form.Set("refresh_token", refreshToken)

	req, _ := http.NewRequestWithContext(ctx, "POST", c.tokenURL(), bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, parseRequestError("refresh", resp)
	}

	var out TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func parseRequestError(operation string, resp *http.Response) error {
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyText := strings.TrimSpace(string(bodyBytes))

	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(bodyBytes, &payload)

	return &RequestError{
		Operation:        operation,
		StatusCode:       resp.StatusCode,
		ErrorCode:        strings.TrimSpace(payload.Error),
		ErrorDescription: strings.TrimSpace(payload.ErrorDescription),
		Body:             bodyText,
	}
}

func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	return c.ExchangeCodePKCE(ctx, code, redirectURI, "")
}

func (c *Client) ExchangeCodePKCE(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", c.cid)
	if c.secret != "" {
		form.Set("client_secret", c.secret)
	}
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(codeVerifier) != "" {
		form.Set("code_verifier", codeVerifier)
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.tokenURL(), bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("keycloak code exchange failed: status=%d, body=%s", resp.StatusCode, string(bodyBytes))
	}

	var out TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Logout(ctx context.Context, refreshToken string) error {
	form := url.Values{}
	form.Set("client_id", c.cid)
	if c.secret != "" {
		form.Set("client_secret", c.secret)
	}
	form.Set("refresh_token", refreshToken)

	req, _ := http.NewRequestWithContext(ctx, "POST", c.logoutURL(), bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("keycloak logout failed: status=%d", resp.StatusCode)
	}
	return nil
}

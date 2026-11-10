package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const growyTenantProvisionTTL = 30 * time.Second

// ProvisionGrowyTenant creates or reconciles the matching Growy tenant using
// a short-lived assertion signed by Workspace. Growy treats the organization
// UUID as an idempotency key, so retries cannot create duplicate tenants.
func (s *OrganizationService) ProvisionGrowyTenant(ctx context.Context, organizationID uuid.UUID, name, slug string) error {
	endpoint, err := growyTenantProvisionEndpoint()
	if err != nil {
		return err
	}
	return s.provisionGrowyTenant(ctx, organizationID, name, slug, func(body io.Reader) (*http.Request, error) {
		if strings.TrimSpace(os.Getenv("GROWY_TENANT_PROVISION_URL")) != "" {
			return http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
		}
		publicBase := strings.TrimSuffix(endpoint, "/api/token/workspace/provision-tenant/")
		return NewGrowyAPIRequest(ctx, http.MethodPost, publicBase, "/api/token/workspace/provision-tenant/", body)
	})
}

// Provision the same immutable Workspace organization in the LMS deployment,
// independently of any CRM-specific provisioning endpoint.
func (s *OrganizationService) ProvisionGrowyLMSTenant(ctx context.Context, organizationID uuid.UUID, name, slug, publicBase string) error {
	return s.provisionGrowyTenant(ctx, organizationID, name, slug, func(body io.Reader) (*http.Request, error) {
		return NewGrowyLMSAPIRequest(ctx, http.MethodPost, publicBase, "/api/token/workspace/provision-tenant/", slug, body)
	})
}

func (s *OrganizationService) provisionGrowyTenant(ctx context.Context, organizationID uuid.UUID, name, slug string, requestForBody func(io.Reader) (*http.Request, error)) error {
	encodedKey := strings.TrimSpace(os.Getenv("GROWY_SSO_PRIVATE_KEY_DER_B64"))
	if encodedKey == "" {
		return fmt.Errorf("Growy tenant provisioning signing key is not configured")
	}
	privateDER, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return fmt.Errorf("decode Growy tenant provisioning signing key: %w", err)
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(privateDER)
	if err != nil {
		return fmt.Errorf("parse Growy tenant provisioning signing key: %w", err)
	}
	privateKey, ok := parsedKey.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("Growy tenant provisioning signing key must be Ed25519")
	}

	issuer := strings.TrimSpace(os.Getenv("GROWY_SSO_ISSUER"))
	if issuer == "" {
		issuer = "https://teamspace.kz"
	}
	now := time.Now()
	ticket, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss":                         issuer,
		"aud":                         "growy-tenant-provision",
		"sub":                         "workspace-organization:" + organizationID.String(),
		"workspace_organization_id":   organizationID.String(),
		"workspace_organization_name": strings.TrimSpace(name),
		"workspace_organization_slug": strings.ToLower(strings.TrimSpace(slug)),
		"token_use":                   "growy_tenant_provision",
		"jti":                         uuid.NewString(),
		"iat":                         now.Unix(),
		"nbf":                         now.Unix(),
		"exp":                         now.Add(growyTenantProvisionTTL).Unix(),
	}).SignedString(privateKey)
	if err != nil {
		return fmt.Errorf("sign Growy tenant provisioning assertion: %w", err)
	}

	body, err := json.Marshal(map[string]string{"ticket": ticket})
	if err != nil {
		return fmt.Errorf("encode Growy tenant provisioning request: %w", err)
	}
	request, err := requestForBody(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Growy tenant provisioning request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")

	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call Growy tenant provisioning endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		return fmt.Errorf("Growy tenant provisioning endpoint returned HTTP %d (%s)", response.StatusCode, growyProvisionFailureReason(body))
	}
	return nil
}

func growyTenantProvisionEndpoint() (string, error) {
	endpoint := strings.TrimSpace(os.Getenv("GROWY_TENANT_PROVISION_URL"))
	if endpoint == "" {
		baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")), "/")
		if baseURL == "" && strings.TrimSpace(os.Getenv("GROWY_PUBLIC_URL")) == "" {
			return "", fmt.Errorf("GROWY_TENANT_PROVISION_URL or APP_PUBLIC_URL must be configured")
		}
		growyURL := strings.TrimRight(strings.TrimSpace(os.Getenv("GROWY_PUBLIC_URL")), "/")
		if growyURL != "" {
			endpoint = growyURL + "/api/token/workspace/provision-tenant/"
		} else {
			endpoint = baseURL + "/growy/api/token/workspace/provision-tenant/"
		}
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Growy tenant provisioning URL is invalid")
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || !(strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback()) {
			return "", fmt.Errorf("Growy tenant provisioning URL must use HTTPS")
		}
	}
	return parsed.String(), nil
}

// Classify known upstream errors without logging assertions, keys or raw response data.
func growyProvisionFailureReason(body []byte) string {
	text := strings.ToLower(string(body))
	for _, failure := range []struct{ message, reason string }{
		{"the organization is disabled", "tenant_disabled"},
		{"a valid organization id and name are required", "invalid_organization_id_or_name"},
		{"invalid or expired workspace ticket", "invalid_or_expired_ticket"},
		{"workspace ticket has already been used", "ticket_already_used"},
		{"workspace sso is not configured", "growy_sso_not_configured"},
		{"workspace sso public key is invalid", "growy_sso_public_key_invalid"},
	} {
		if strings.Contains(text, failure.message) {
			return failure.reason
		}
	}
	return "upstream_rejected"
}

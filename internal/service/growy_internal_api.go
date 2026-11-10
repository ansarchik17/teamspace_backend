package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// The private origin is server configuration, never a model/user URL. Preserve
// Growy's public Host/protocol so Django's tenant and HTTPS checks stay enabled.
func NewGrowyAPIRequest(ctx context.Context, method, publicBase, path string, body io.Reader) (*http.Request, error) {
	return newGrowyAPIRequest(ctx, method, publicBase, path, body, os.Getenv("GROWY_API_INTERNAL_URL"))
}

// LMS has its own deployment, provisioning and credentials. Never route a
// training read through the CRM origin, even when both expose similar paths.
func GrowyLMSPublicBase() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GROWY_LMS_PUBLIC_URL")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")), "/") + "/growy"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Growy LMS public URL")
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return "", fmt.Errorf("Growy LMS public URL must use HTTPS")
	}
	return base, nil
}

var growyDNSLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// organizationSlug must come from the current server-validated membership,
// never a client header or query. Origin supplies Django's tenant context while
// the network destination stays fixed in operator configuration.
func NewGrowyLMSAPIRequest(ctx context.Context, method, publicBase, path, organizationSlug string, body io.Reader) (*http.Request, error) {
	slug := strings.ToLower(strings.TrimSpace(organizationSlug))
	baseDomain := strings.ToLower(strings.TrimSpace(os.Getenv("TENANT_BASE_DOMAIN")))
	if baseDomain == "" {
		baseDomain = "teamspace.kz"
	}
	if !growyDNSLabel.MatchString(slug) || len(baseDomain) > 253 {
		return nil, fmt.Errorf("invalid Growy LMS tenant domain")
	}
	for _, label := range strings.Split(baseDomain, ".") {
		if !growyDNSLabel.MatchString(label) {
			return nil, fmt.Errorf("invalid Growy LMS tenant domain")
		}
	}
	public, err := url.Parse(publicBase)
	if err != nil || public.Host == "" || (public.Scheme != "https" && public.Scheme != "http") {
		return nil, fmt.Errorf("invalid Growy LMS public URL")
	}
	req, err := newGrowyAPIRequest(ctx, method, publicBase, path, body, os.Getenv("GROWY_LMS_INTERNAL_URL"))
	if err != nil {
		return nil, err
	}
	tenantHost := slug + "." + baseDomain
	if public.Port() != "" {
		tenantHost = net.JoinHostPort(tenantHost, public.Port())
	}
	req.Header.Set("Origin", public.Scheme+"://"+tenantHost)
	return req, nil
}

func newGrowyAPIRequest(ctx context.Context, method, publicBase, path string, body io.Reader, internalOrigin string) (*http.Request, error) {
	target := publicBase + path
	internal := strings.TrimRight(strings.TrimSpace(internalOrigin), "/")
	if internal != "" {
		base, err := url.Parse(internal)
		if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" {
			return nil, fmt.Errorf("invalid Growy internal API origin")
		}
		target = internal + path
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if internal != "" {
		public, err := url.Parse(publicBase)
		if err != nil || public.Host == "" || (public.Scheme != "http" && public.Scheme != "https") || public.User != nil || public.RawQuery != "" || public.Fragment != "" {
			return nil, fmt.Errorf("invalid Growy public API origin")
		}
		req.Host = public.Host
		req.Header.Set("X-Forwarded-Proto", public.Scheme)
		req.Header.Set("X-Forwarded-Host", public.Host)
	}
	return req, nil
}

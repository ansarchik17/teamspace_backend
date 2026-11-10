package domain

import (
	"net/url"
	"strings"
)

// Only documented connection parameters can be supplied or returned. Secrets
// use the dedicated request field and are stored exclusively in Keycloak.
func IdentityProviderSettingKeys(providerType string) []string {
	if providerType == IdentityProviderOIDC {
		return []string{"authorizationUrl", "tokenUrl", "userInfoUrl", "logoutUrl", "clientId", "issuer", "jwksUrl", "defaultScope", "clientAuthMethod", "validateSignature", "useJwksUrl"}
	}
	if providerType == IdentityProviderSAML {
		return []string{"singleSignOnServiceUrl", "entityId", "singleLogoutServiceUrl", "signingCertificate", "nameIDPolicyFormat", "validateSignature"}
	}
	return nil
}

func canonicalProviderSetting(providerType, key string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "_", ""))
	for _, candidate := range IdentityProviderSettingKeys(providerType) {
		if strings.ToLower(candidate) == normalized {
			return candidate
		}
	}
	return ""
}

func NormalizeIdentityProviderSettings(providerType string, settings map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for key, raw := range settings {
		canonical := canonicalProviderSetting(providerType, key)
		value := strings.TrimSpace(raw)
		if canonical == "" || len([]rune(raw)) > 4096 {
			return nil, ErrIdentityProviderSettings
		}
		if old, exists := out[canonical]; exists && old != value {
			return nil, ErrIdentityProviderSettings
		}
		if value != "" && (strings.HasSuffix(canonical, "Url") || canonical == "issuer") {
			u, err := url.Parse(value)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
				return nil, ErrIdentityProviderSettings
			}
			for key := range u.Query() {
				lower := strings.ToLower(strings.ReplaceAll(key, "_", ""))
				if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") || lower == "token" || lower == "accesstoken" || lower == "apikey" {
					return nil, ErrIdentityProviderSettings
				}
			}
		}
		if (canonical == "validateSignature" || canonical == "useJwksUrl") && value != "true" && value != "false" {
			return nil, ErrIdentityProviderSettings
		}
		out[canonical] = value
	}
	return out, nil
}

func (p OrganizationIdentityProvider) Public() OrganizationIdentityProvider {
	safe := map[string]string{}
	for key, value := range p.Settings {
		if canonical := canonicalProviderSetting(p.ProviderType, key); canonical != "" {
			if normalized, err := NormalizeIdentityProviderSettings(p.ProviderType, map[string]string{canonical: value}); err == nil {
				safe[canonical] = normalized[canonical]
			}
		}
	}
	p.Settings = safe
	p.LastError = ""
	return p
}

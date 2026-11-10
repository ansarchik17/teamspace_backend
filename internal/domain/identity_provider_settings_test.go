package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderPublicSettingsNeverExposeSecretsOrDiagnostics(t *testing.T) {
	p := OrganizationIdentityProvider{ProviderType: IdentityProviderOIDC, SecretSet: true, LastError: "private-diagnostic", Settings: map[string]string{"clientSecret": "secret-one", "client_secret": "secret-two", "password": "secret-three", "privateKey": "secret-four", "client_id": "public-client", "tokenUrl": "https://user:secret-five@example.com/token"}}
	b, err := json.Marshal(p.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-one", "secret-two", "secret-three", "secret-four", "secret-five", "private-diagnostic", "last_error"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("sensitive field leaked: %s", b)
		}
	}
	if !strings.Contains(string(b), "public-client") {
		t.Fatal("public configuration missing")
	}
}

func TestProviderSettingsRejectUnsafeOrAmbiguousInput(t *testing.T) {
	for _, settings := range []map[string]string{{"client_secret": "secret"}, {"unknownOption": "value"}, {"tokenUrl": "file:///private"}, {"tokenUrl": "https://user:password@example.com"}, {"client_id": "one", "clientId": "two"}, {"validateSignature": "maybe"}} {
		if _, err := NormalizeIdentityProviderSettings(IdentityProviderOIDC, settings); err == nil {
			t.Fatalf("invalid settings accepted: %v", settings)
		}
	}
	s, err := NormalizeIdentityProviderSettings(IdentityProviderOIDC, map[string]string{"client_id": " example ", "token_url": "https://example.com/token"})
	if err != nil || s["clientId"] != "example" {
		t.Fatalf("normalization failed: %v", err)
	}
}

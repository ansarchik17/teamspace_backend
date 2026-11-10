package auth

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/keycloak"
)

func TestExtractAZPFromJWT(t *testing.T) {
	t.Parallel()

	token := "eyJhbGciOiJub25lIn0.eyJhenAiOiJhbGVtLW1vYmlsZSJ9."
	if got := extractAZPFromJWT(token); got != "alem-mobile" {
		t.Fatalf("extractAZPFromJWT() = %q, want %q", got, "alem-mobile")
	}
}

func TestResolveKeycloakClientForTokenUsesMappedClient(t *testing.T) {
	t.Parallel()

	defaultClient := keycloak.New("https://sso.example.com", "realm", "alem-workspace", "secret")
	mobileClient := keycloak.New("https://sso.example.com", "realm", "alem-mobile", "")

	resolved := resolveKeycloakClientForToken(
		"eyJhbGciOiJub25lIn0.eyJhenAiOiJhbGVtLW1vYmlsZSJ9.",
		defaultClient,
		map[string]*keycloak.Client{
			"alem-mobile": mobileClient,
		},
	)

	if resolved != mobileClient {
		t.Fatal("resolveKeycloakClientForToken() did not return mapped mobile client")
	}
}

package auth

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/keycloak"
)

func ptr(s string) *string { return &s }

func makeClients() (defaultClient, mobileClient, workspaceClient *keycloak.Client, clients map[string]*keycloak.Client) {
	defaultClient = keycloak.New("https://sso.example.com", "realm", "alem-workspace", "secret")
	mobileClient = keycloak.New("https://sso.example.com", "realm", "alem-mobile", "")
	workspaceClient = keycloak.New("https://sso.example.com", "realm", "alem-workspace", "secret")
	clients = map[string]*keycloak.Client{
		"alem-mobile":    mobileClient,
		"alem-workspace": workspaceClient,
	}
	return
}

const jwtWithAlemMobile = "eyJhbGciOiJub25lIn0.eyJhenAiOiJhbGVtLW1vYmlsZSJ9."
const opaqueToken = "opaque-refresh-token-no-dots-or-invalid-jwt"

func TestResolveClientForPinLogin_StoredMobileClientID(t *testing.T) {
	t.Parallel()
	defaultClient, mobileClient, _, clients := makeClients()

	got, err := resolveClientForPinLogin(ptr("alem-mobile"), opaqueToken, "", defaultClient, clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mobileClient {
		t.Fatal("expected mobile client")
	}
}

func TestResolveClientForPinLogin_StoredWorkspaceClientID(t *testing.T) {
	t.Parallel()
	defaultClient, _, workspaceClient, clients := makeClients()

	got, err := resolveClientForPinLogin(ptr("alem-workspace"), opaqueToken, "", defaultClient, clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != workspaceClient {
		t.Fatal("expected workspace client")
	}
}

func TestResolveClientForPinLogin_StoredUnknownClientID_ReturnsError(t *testing.T) {
	t.Parallel()
	defaultClient, _, _, clients := makeClients()

	_, err := resolveClientForPinLogin(ptr("unknown-client"), opaqueToken, "", defaultClient, clients)
	if err == nil {
		t.Fatal("expected error for unknown client_id, got nil")
	}
}

func TestResolveClientForPinLogin_NilStoredID_FallbackToJWT(t *testing.T) {
	t.Parallel()
	defaultClient, mobileClient, _, clients := makeClients()

	got, err := resolveClientForPinLogin(nil, jwtWithAlemMobile, "", defaultClient, clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mobileClient {
		t.Fatal("expected mobile client from JWT azp fallback")
	}
}

func TestResolveClientForPinLogin_NilStoredID_OpaqueToken_FallbackToDefaultClientID(t *testing.T) {
	t.Parallel()
	defaultClient, mobileClient, _, clients := makeClients()

	got, err := resolveClientForPinLogin(nil, opaqueToken, "alem-mobile", defaultClient, clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mobileClient {
		t.Fatal("expected mobile client from defaultClientID fallback")
	}
}

func TestResolveClientForPinLogin_NilStoredID_OpaqueToken_NoDefaultClientID_ReturnsDefaultClient(t *testing.T) {
	t.Parallel()
	defaultClient, _, _, clients := makeClients()

	got, err := resolveClientForPinLogin(nil, opaqueToken, "", defaultClient, clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != defaultClient {
		t.Fatal("expected defaultClient as last resort")
	}
}

func TestResolveClientForPinLogin_DefaultClientIDNotConfigured_ReturnsError(t *testing.T) {
	t.Parallel()
	defaultClient, _, _, clients := makeClients()

	_, err := resolveClientForPinLogin(nil, opaqueToken, "not-in-map", defaultClient, clients)
	if err == nil {
		t.Fatal("expected error when defaultClientID not found in map")
	}
}

package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestProvisionGrowyTenantSignsAndSendsCompanyAssertion(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("encode signing key: %v", err)
	}
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", base64.StdEncoding.EncodeToString(privateDER))
	t.Setenv("GROWY_SSO_ISSUER", "https://workspace.test")
	organizationID := uuid.New()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var requestBody map[string]string
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token, err := jwt.Parse(requestBody["ticket"], func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodEdDSA {
				t.Errorf("signing method = %s, want EdDSA", token.Method.Alg())
			}
			return publicKey, nil
		})
		if err != nil {
			t.Errorf("parse provisioning ticket: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok || claims["aud"] != "growy-tenant-provision" || claims["token_use"] != "growy_tenant_provision" || claims["workspace_organization_id"] != organizationID.String() || claims["workspace_organization_name"] != "Example Company" || claims["workspace_organization_slug"] != "example-company" {
			t.Errorf("unexpected provisioning claims: %#v", token.Claims)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	t.Setenv("GROWY_TENANT_PROVISION_URL", server.URL)

	service := &OrganizationService{}
	if err := service.ProvisionGrowyTenant(t.Context(), organizationID, "Example Company", "example-company"); err != nil {
		t.Fatalf("provision Growy tenant: %v", err)
	}
}

func TestGrowyTenantProvisionEndpointUsesWorkspacePublicURL(t *testing.T) {
	t.Setenv("GROWY_TENANT_PROVISION_URL", "")
	t.Setenv("APP_PUBLIC_URL", "https://teamspace.kz")

	endpoint, err := growyTenantProvisionEndpoint()
	if err != nil {
		t.Fatalf("resolve provision endpoint: %v", err)
	}
	want := "https://teamspace.kz/growy/api/token/workspace/provision-tenant/"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
}

func TestProvisionGrowyLMSTenantUsesLMSAndSignsSameOrganization(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", base64.StdEncoding.EncodeToString(privateDER))
	t.Setenv("TENANT_BASE_DOMAIN", "teamspace.kz")
	organizationID := uuid.New()
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("LMS provisioning reached CRM")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer crm.Close()
	t.Setenv("GROWY_TENANT_PROVISION_URL", crm.URL)
	t.Setenv("GROWY_API_INTERNAL_URL", crm.URL)
	calls := 0
	lms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/token/workspace/provision-tenant/" || r.Host != "teamspace.kz" || r.Header.Get("Origin") != "https://sultan-corp.teamspace.kz" {
			t.Error("LMS provisioning lost the target or tenant context")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		token, err := jwt.Parse(body["ticket"], func(token *jwt.Token) (any, error) { return publicKey, nil }, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithAudience("growy-tenant-provision"), jwt.WithExpirationRequired())
		if err != nil {
			t.Error(err)
			return
		}
		claims := token.Claims.(jwt.MapClaims)
		if claims["workspace_organization_id"] != organizationID.String() || claims["workspace_organization_slug"] != "sultan-corp" || claims["token_use"] != "growy_tenant_provision" {
			t.Error("LMS provisioning changed the organization")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer lms.Close()
	t.Setenv("GROWY_LMS_INTERNAL_URL", lms.URL)
	service := &OrganizationService{}
	if err := service.ProvisionGrowyLMSTenant(t.Context(), organizationID, "Sultan Corp", "sultan-corp", "https://teamspace.kz/growy"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("LMS provisioning calls = %d", calls)
	}
}

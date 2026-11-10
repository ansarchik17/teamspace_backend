package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestSignGrowyUserTicketContract(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", base64.StdEncoding.EncodeToString(der))
	t.Setenv("GROWY_SSO_ISSUER", "https://workspace.test")
	id := uuid.New()
	ticket, err := SignGrowyUserTicket(123, "person@example.test", "person", "Person", "Example", &domain.GrowyHandoffDetails{OrganizationID: id, GrowyRole: "manager", GrowyRoles: []string{"manager", "employee", "issuer"}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Parse(ticket, func(token *jwt.Token) (any, error) { return public, nil }, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithAudience("growy"), jwt.WithIssuer("https://workspace.test"))
	if err != nil {
		t.Fatal(err)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["sub"] != "workspace:123" || claims["workspace_organization_id"] != id.String() || claims["growy_role"] != "manager" || claims["token_use"] != "growy_handoff" || claims["exp"].(float64)-claims["iat"].(float64) != 45 {
		t.Fatal("Unexpected Growy ticket contract")
	}
	roles, ok := claims["growy_roles"].([]any)
	if !ok || len(roles) != 3 || roles[0] != "manager" || roles[1] != "employee" || roles[2] != "issuer" {
		t.Fatalf("ticket did not preserve assigned roles: %v", claims["growy_roles"])
	}
}

func TestSignGrowyUserTicketFailsWithoutConfiguredKey(t *testing.T) {
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", "")
	if _, err := SignGrowyUserTicket(123, "person@example.test", "person", "", "", &domain.GrowyHandoffDetails{}); err == nil {
		t.Fatal("Missing signing key accepted")
	}
}

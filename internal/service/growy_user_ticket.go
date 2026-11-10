package service

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// SignGrowyUserTicket accepts only identity resolved from authenticated server
// context and organization details from the database, never a client body.
func SignGrowyUserTicket(userID int64, email, username, firstName, lastName string, details *domain.GrowyHandoffDetails) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("GROWY_SSO_PRIVATE_KEY_DER_B64")))
	if err != nil {
		return "", fmt.Errorf("Growy signing key is invalid")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("Growy signing key is not configured")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return "", fmt.Errorf("Growy signing key must be Ed25519")
	}
	issuer := strings.TrimSpace(os.Getenv("GROWY_SSO_ISSUER"))
	if issuer == "" {
		issuer = "https://teamspace.kz"
	}
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": issuer, "aud": "growy", "sub": fmt.Sprintf("workspace:%d", userID),
		"email": strings.ToLower(strings.TrimSpace(email)), "preferred_username": strings.TrimSpace(username),
		"given_name": strings.TrimSpace(firstName), "family_name": strings.TrimSpace(lastName),
		"workspace_organization_id": details.OrganizationID.String(), "workspace_organization_slug": details.OrganizationSlug,
		"position": details.Position, "growy_role": details.GrowyRole, "token_use": "growy_handoff",
		"growy_roles": details.AssignedGrowyRoles(),
		"jti":         uuid.NewString(), "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(45 * time.Second).Unix(),
	}).SignedString(key)
}

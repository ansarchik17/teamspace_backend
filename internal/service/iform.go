package service

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) IFormContext(ctx context.Context, actor int64, expected uuid.UUID) (*domain.IFormContext, error) {
	return s.repo.IFormContext(ctx, actor, expected)
}
func (s *OrganizationService) IFormOnboardingEmployee(ctx context.Context, company, requestID uuid.UUID, actor int64) (int64, error) {
	return s.repo.IFormOnboardingEmployee(ctx, company, requestID, actor)
}
func (s *OrganizationService) IFormIdentity(ctx context.Context, actor int64) (*domain.IFormContext, error) {
	return s.repo.IFormIdentity(ctx, actor)
}

func (s *OrganizationService) IFormOperationReceipt(ctx context.Context, actor int64, op domain.IFormOperation, run func() (json.RawMessage, error)) (json.RawMessage, error) {
	encoded, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return s.repo.IFormOperationReceipt(ctx, actor, op, hex.EncodeToString(hash[:]), run)
}

// Tokens remain between servers. They contain stable IDs, never user supplied roles.
func SignIFormRequest(context *domain.IFormContext, method, path string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("IFORM_SSO_PRIVATE_KEY_DER_B64")))
	if err != nil {
		return "", fmt.Errorf("invalid iForm key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return "", fmt.Errorf("iForm signing key is not configured")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 {
		return "", fmt.Errorf("iForm signing key must be RSA 2048 or stronger")
	}
	issuer := strings.TrimSpace(os.Getenv("IFORM_SSO_ISSUER"))
	if issuer == "" || context.OrganizationID == uuid.Nil || !strings.HasPrefix(context.Actor, "workspace:") {
		return "", fmt.Errorf("iForm identity configuration is incomplete")
	}
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer, "aud": "iform-api", "sub": context.Actor, "workspace_organization_id": context.OrganizationID.String(),
		"token_use": "iform_request", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(30 * time.Second).Unix(), "jti": uuid.NewString(),
		"request_method": method, "request_path": path,
	}).SignedString(key)
}

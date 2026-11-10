package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestIFormSignedServerRequest(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("IFORM_SSO_PRIVATE_KEY_DER_B64", base64.StdEncoding.EncodeToString(der))
	t.Setenv("IFORM_SSO_ISSUER", "https://workspace.test")
	ctx := &domain.IFormContext{OrganizationID: uuid.New(), Actor: "workspace:12"}
	raw, err := SignIFormRequest(ctx, "GET", "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience("iform-api"), jwt.WithIssuer("https://workspace.test"))
	if err != nil {
		t.Fatal(err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != ctx.Actor || claims["workspace_organization_id"] != ctx.OrganizationID.String() || claims["request_path"] != "/api/v1/me" || claims["request_method"] != "GET" || claims["exp"].(float64)-claims["iat"].(float64) != 30 {
		t.Fatal("invalid contract")
	}
	if _, ok := claims["roles"]; ok {
		t.Fatal("request token must not carry stale role grants")
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err = jwt.Parse(raw, func(*jwt.Token) (any, error) { return &other.PublicKey, nil }); err == nil {
		t.Fatal("foreign signature accepted")
	}
	t.Setenv("IFORM_SSO_PRIVATE_KEY_DER_B64", "")
	if _, err = SignIFormRequest(ctx, "GET", "/api/v1/me"); err == nil {
		t.Fatal("missing key accepted")
	}
}

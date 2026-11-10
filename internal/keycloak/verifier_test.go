package keycloak

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifierValidateClaimsAllowsConfiguredAZP(t *testing.T) {
	t.Parallel()

	verifier := &Verifier{
		issuer: "https://sso.example.com/realms/test",
		trustedAudiences: map[string]struct{}{
			"alem-workspace": {},
			"growy-client":   {},
		},
		allowedAZP: map[string]struct{}{
			"alem-workspace": {},
			"alem-mobile":    {},
		},
	}

	err := verifier.validateClaims(jwt.MapClaims{
		"iss": "https://sso.example.com/realms/test",
		"aud": []any{"account", "growy-client"},
		"azp": "alem-mobile",
	})
	if err != nil {
		t.Fatalf("validateClaims() unexpected error = %v", err)
	}
}

func TestVerifierValidateClaimsRejectsUnknownAZP(t *testing.T) {
	t.Parallel()

	verifier := &Verifier{
		issuer: "https://sso.example.com/realms/test",
		trustedAudiences: map[string]struct{}{
			"alem-workspace": {},
			"growy-client":   {},
		},
		allowedAZP: map[string]struct{}{
			"alem-workspace": {},
			"growy-client":   {},
		},
	}

	err := verifier.validateClaims(jwt.MapClaims{
		"iss": "https://sso.example.com/realms/test",
		"aud": []any{"alem-workspace"},
		"azp": "unknown-client",
	})
	if err == nil {
		t.Fatal("validateClaims() expected error for unknown azp")
	}
}

func TestVerifierRejectsMissingAudience(t *testing.T) {
	t.Parallel()

	verifier := &Verifier{
		trustedAudiences: map[string]struct{}{
			"alem-workspace": {},
		},
	}

	err := verifier.validateClaims(jwt.MapClaims{
		"iss": "https://sso.example.com/realms/test",
	})
	if err == nil {
		t.Fatal("validateClaims() expected error for missing audience")
	}
}

func TestVerifierRejectsAudienceOutsideTrustedSet(t *testing.T) {
	t.Parallel()

	verifier := &Verifier{
		trustedAudiences: map[string]struct{}{
			"alem-workspace": {},
		},
	}

	err := verifier.validateClaims(jwt.MapClaims{
		"iss": "https://sso.example.com/realms/test",
		"aud": []any{"growy-client"},
	})
	if err == nil {
		t.Fatal("validateClaims() expected error for invalid audience")
	}
}

func TestVerifierAllowsTrustedAudienceWithoutAZPCheck(t *testing.T) {
	t.Parallel()

	verifier := &Verifier{
		issuer: "https://sso.example.com/realms/test",
		trustedAudiences: map[string]struct{}{
			"alem-workspace": {},
			"growy-client":   {},
		},
	}

	err := verifier.validateClaims(jwt.MapClaims{
		"iss": "https://sso.example.com/realms/test",
		"aud": []any{"account", "growy-client"},
		"azp": "different-client",
	})
	if err != nil {
		t.Fatalf("validateClaims() unexpected error = %v", err)
	}
}

func TestVerifierTrustedAudiencesReturnsSortedValues(t *testing.T) {
	t.Parallel()

	verifier := &Verifier{
		trustedAudiences: map[string]struct{}{
			"alem-workspace": {},
			"alem-mobile":    {},
		},
	}

	got := verifier.TrustedAudiences()
	want := []string{"alem-mobile", "alem-workspace"}
	if len(got) != len(want) {
		t.Fatalf("TrustedAudiences() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TrustedAudiences()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

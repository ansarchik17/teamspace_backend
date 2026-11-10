package keycloak

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Verifier struct {
	jwks             *JWKSCache
	issuer           string
	trustedAudiences map[string]struct{}
	allowedAZP       map[string]struct{}
}

// ForServiceClient shares the trusted issuer and JWKS cache, but does not widen
// the browser verifier's accepted audiences or clients.
func (v *Verifier) ForServiceClient(audience, clientID string) *Verifier {
	if v == nil || v.jwks == nil || v.issuer == "" || strings.TrimSpace(audience) == "" || strings.TrimSpace(clientID) == "" {
		return nil
	}
	return &Verifier{jwks: v.jwks, issuer: v.issuer,
		trustedAudiences: map[string]struct{}{audience: {}}, allowedAZP: map[string]struct{}{clientID: {}}}
}

var (
	ErrMissingAudience         = errors.New("missing audience")
	ErrInvalidAudience         = errors.New("invalid audience")
	ErrInvalidAuthorizedParty  = errors.New("invalid authorized party")
	ErrInvalidIssuer           = errors.New("invalid issuer")
	ErrRefreshTokenNotAccepted = errors.New("refresh token is not accepted")
)

func NewVerifier(ctx context.Context, jwksURL, issuer string, trustedAudiences, allowedAZP []string) (*Verifier, error) {
	cache := NewJWKSCache(jwksURL, time.Hour)
	if err := cache.ensure(ctx); err != nil {
		return nil, err
	}
	trustedAudienceSet := make(map[string]struct{}, len(trustedAudiences))
	for _, audience := range trustedAudiences {
		audience = strings.TrimSpace(audience)
		if audience == "" {
			continue
		}
		trustedAudienceSet[audience] = struct{}{}
	}
	allowedAZPSet := make(map[string]struct{}, len(allowedAZP))
	for _, azp := range allowedAZP {
		azp = strings.TrimSpace(azp)
		if azp == "" {
			continue
		}
		allowedAZPSet[azp] = struct{}{}
	}
	return &Verifier{
		jwks:             cache,
		issuer:           issuer,
		trustedAudiences: trustedAudienceSet,
		allowedAZP:       allowedAZPSet,
	}, nil
}

func (v *Verifier) Parse(ctx context.Context, tokenString string) (*jwt.Token, jwt.MapClaims, error) {
	claims := jwt.MapClaims{}

	tok, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		v.jwks.Keyfunc(ctx),
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithLeeway(30*time.Second),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, nil, err
	}
	if !tok.Valid {
		return nil, nil, fmt.Errorf("token invalid")
	}

	if err := v.validateClaims(claims); err != nil {
		return nil, nil, err
	}

	return tok, claims, nil
}

func (v *Verifier) validateClaims(claims jwt.MapClaims) error {
	if v.issuer != "" {
		iss, _ := claims["iss"].(string)
		if iss != v.issuer {
			return fmt.Errorf("%w: got=%q want=%q", ErrInvalidIssuer, iss, v.issuer)
		}
	}

	if isRefreshToken(claims) {
		return ErrRefreshTokenNotAccepted
	}

	audiences := extractAudiences(claims["aud"])
	if len(audiences) == 0 {
		return ErrMissingAudience
	}
	if len(v.trustedAudiences) > 0 && !hasAudienceIntersection(audiences, v.trustedAudiences) {
		return fmt.Errorf("%w: got=%v allowed=%v", ErrInvalidAudience, audiences, v.TrustedAudiences())
	}

	if len(v.allowedAZP) > 0 {
		azp, _ := claims["azp"].(string)
		if _, ok := v.allowedAZP[azp]; !ok {
			return fmt.Errorf("%w: got=%q allowed=%v", ErrInvalidAuthorizedParty, azp, v.AllowedAZP())
		}
	}

	return nil
}

func (v *Verifier) TrustedAudiences() []string {
	audiences := make([]string, 0, len(v.trustedAudiences))
	for audience := range v.trustedAudiences {
		audiences = append(audiences, audience)
	}
	sort.Strings(audiences)
	return audiences
}

func (v *Verifier) AllowedAZP() []string {
	values := make([]string, 0, len(v.allowedAZP))
	for azp := range v.allowedAZP {
		values = append(values, azp)
	}
	sort.Strings(values)
	return values
}

func extractAudiences(raw any) []string {
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		return []string{value}
	case []string:
		out := make([]string, 0, len(value))
		for _, audience := range value {
			audience = strings.TrimSpace(audience)
			if audience != "" {
				out = append(out, audience)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			audience, ok := item.(string)
			if !ok {
				continue
			}
			audience = strings.TrimSpace(audience)
			if audience != "" {
				out = append(out, audience)
			}
		}
		return out
	default:
		return nil
	}
}

func ExtractAudiencesForLog(raw any) []string {
	return extractAudiences(raw)
}

func hasAudienceIntersection(audiences []string, allowed map[string]struct{}) bool {
	for _, audience := range audiences {
		if _, ok := allowed[audience]; ok {
			return true
		}
	}
	return false
}

func isRefreshToken(claims jwt.MapClaims) bool {
	for _, key := range []string{"token_type", "typ"} {
		value, _ := claims[key].(string)
		if strings.EqualFold(strings.TrimSpace(value), "refresh") {
			return true
		}
	}
	return false
}

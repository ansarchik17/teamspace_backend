package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

func TestODOAssignmentsServiceAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "odo-key", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
	}))
	defer jwks.Close()
	verifier, err := keycloak.NewVerifier(context.Background(), jwks.URL, "https://identity.example.test/realms/Workspace", []string{"browser-api"}, []string{"browser-client"})
	require.NoError(t, err)
	m := NewKeycloakAuthMiddleware(verifier, nil, nil)
	cfg := config.InternalConfig{ODOAssignmentsEnabled: true, ODOAssignmentsClientID: "odo-sync", ODOAssignmentsSubject: "service-sub", ODOAssignmentsAudience: "workspace-odo-assignments", ODOAssignmentsCompanies: "11111111-1111-4111-8111-111111111111:10"}
	for _, test := range []struct {
		name   string
		change func(jwt.MapClaims)
		code   int
		method jwt.SigningMethod
	}{
		{"allowed service", func(jwt.MapClaims) {}, 200, jwt.SigningMethodRS256},
		{"wrong issuer", func(c jwt.MapClaims) { c["iss"] = "https://other.example.test" }, 401, jwt.SigningMethodRS256},
		{"wrong audience", func(c jwt.MapClaims) { c["aud"] = "browser-api" }, 401, jwt.SigningMethodRS256},
		{"wrong client", func(c jwt.MapClaims) { c["azp"] = "browser-client" }, 401, jwt.SigningMethodRS256},
		{"ordinary admin user", func(c jwt.MapClaims) {
			c["sub"] = "human-admin"
			c["realm_access"] = map[string]any{"roles": []string{"admin"}}
		}, 401, jwt.SigningMethodRS256},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, 401, jwt.SigningMethodRS256},
		{"no expiry", func(c jwt.MapClaims) { delete(c, "exp") }, 401, jwt.SigningMethodRS256},
		{"refresh", func(c jwt.MapClaims) { c["typ"] = "Refresh" }, 401, jwt.SigningMethodRS256},
		{"local HS256 token", func(jwt.MapClaims) {}, 401, jwt.SigningMethodHS256},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := jwt.MapClaims{"iss": "https://identity.example.test/realms/Workspace", "aud": "workspace-odo-assignments", "azp": "odo-sync", "sub": "service-sub", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
			test.change(claims)
			token := jwt.NewWithClaims(test.method, claims)
			token.Header["kid"] = "odo-key"
			var signer any = key
			if test.method == jwt.SigningMethodHS256 {
				signer = []byte("test-only-local-secret")
			}
			signed, err := token.SignedString(signer)
			require.NoError(t, err)
			router := gin.New()
			router.POST("/sync", m.AuthenticateODOAssignments(cfg), func(c *gin.Context) {
				_, userExists := c.Get("user_id")
				require.False(t, userExists)
				principal, exists := c.Get(ODOAssignmentPrincipalKey)
				require.True(t, exists)
				require.Equal(t, "service-sub", principal.(ODOAssignmentPrincipal).Subject)
				c.Status(200)
			})
			r := httptest.NewRequest(http.MethodPost, "/sync", nil)
			r.Header.Set("Authorization", "Bearer "+signed)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, test.code, w.Code)
		})
	}
	for _, companies := range []string{"", "*", "invalid:1", "11111111-1111-4111-8111-111111111111:0", "11111111-1111-4111-8111-111111111111:1,11111111-1111-4111-8111-111111111111:2"} {
		_, err := ParseODOAssignmentCompanies(companies)
		require.Error(t, err)
	}
	router := gin.New()
	router.POST("/sync", (*KeycloakAuthMiddleware)(nil).AuthenticateODOAssignments(config.InternalConfig{}), func(c *gin.Context) { t.Fatal("disabled integration reached handler") })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sync", nil))
	require.Equal(t, 403, w.Code)
}

package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type admissionKCRepo struct{ *repository.UserRepository }

func (r admissionKCRepo) UpsertFromKeycloak(_ context.Context, u *domain.User) error {
	u.ID = 7
	return nil
}
func (r admissionKCRepo) UpdateLastLogin(context.Context, int64, time.Time) error { return nil }

func TestEmploymentAdmissionGuardsExistingLocalTokensDirectKeycloakAndRefresh(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("use disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "admission_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id uuid PRIMARY KEY);CREATE TABLE users(id bigint PRIMARY KEY,username text,is_active boolean,organization_id uuid,deleted_at timestamptz);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261022120000_employment_admission.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	company, request := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1);`, company)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users VALUES(7,'onb.synthetic',true,$1,null);`, company)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date) VALUES($1,$2,7,7,CURRENT_DATE-2)`, company, request)
	require.NoError(t, err)
	repo := repository.NewUserRepository(db, zap.NewNop())
	tm := auth.NewTokenManager(&config.JWTConfig{Secret: "synthetic-token-key", Issuer: "workspace-test", Audience: "test", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour}, nil)
	tm.SetUserActiveLookup(repo.IsUserActive)
	pair, err := tm.GenerateTokenPair(ctx, 7, "", "", "", "", "", false, nil)
	require.NoError(t, err)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "hire", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
	}))
	defer jwks.Close()
	verifier, err := keycloak.NewVerifier(ctx, jwks.URL, "https://sso.test", []string{"workspace"}, []string{"web"})
	require.NoError(t, err)
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://sso.test", "sub": "hire", "aud": "workspace", "azp": "web", "exp": time.Now().Add(time.Hour).Unix()})
	jwtToken.Header["kid"] = "hire"
	raw, err := jwtToken.SignedString(key)
	require.NoError(t, err)
	for _, state := range []string{"future", "allowed", "cancelled"} {
		if state == "future" {
			_, err = db.Exec(ctx, `UPDATE employee_admissions SET start_date=CURRENT_DATE+2`)
		} else if state == "allowed" {
			_, err = db.Exec(ctx, `UPDATE employee_admissions SET start_date=CURRENT_DATE-2`)
		} else {
			_, err = db.Exec(ctx, `UPDATE employee_admissions SET cancelled_at=now()`)
		}
		require.NoError(t, err)
		for _, entry := range []struct {
			token      string
			middleware gin.HandlerFunc
		}{{pair.AccessToken, NewAuthMiddleware(tm, zap.NewNop()).Authenticate()}, {pair.AccessToken, NewKeycloakAuthMiddleware(verifier, admissionKCRepo{repo}, zap.NewNop(), tm).Authenticate()}, {raw, NewKeycloakAuthMiddleware(verifier, admissionKCRepo{repo}, zap.NewNop(), tm).Authenticate()}} {
			router := gin.New()
			router.GET("/api/protected", entry.middleware, func(c *gin.Context) { c.Status(200) })
			r := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
			r.Header.Set("Authorization", "Bearer "+entry.token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if state == "allowed" {
				require.Equal(t, 200, w.Code, state+" "+w.Body.String())
			} else {
				require.Equal(t, 403, w.Code, state+" "+w.Body.String())
			}
		}
		_, err = tm.RefreshAccessToken(ctx, pair.RefreshToken)
		if state == "allowed" {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, auth.ErrUserInactive)
		}
	}
}

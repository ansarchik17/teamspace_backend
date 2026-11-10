package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestGrowyHTTPModuleGatesAndRevocationAreIndependent(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "crm_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, cleanupErr := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		require.NoError(t, cleanupErr)
	})
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,name TEXT,slug TEXT,status TEXT,owner_id BIGINT);
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,position TEXT,is_active BOOLEAN,deleted_at TIMESTAMPTZ);
CREATE TABLE organization_growy_position_roles(organization_id UUID,position_key TEXT,growy_role TEXT,growy_roles TEXT[]);`)
	require.NoError(t, err)
	org := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'Module test','module-test','active',2)`, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users VALUES(2,$1,'Director',true,NULL)`, org)
	require.NoError(t, err)
	for _, name := range []string{"20261012000300_company_modules.sql", "20261103000100_crm_company_module.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", base64.StdEncoding.EncodeToString(der))
	t.Setenv("GROWY_TENANT_PROVISION_URL", "")
	t.Setenv("TENANT_BASE_DOMAIN", "teamspace.kz")
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Minute).Unix()}).SignedString([]byte("local-test-key"))
	require.NoError(t, err)
	var crmCalls, lmsCalls atomic.Int64
	upstream := func(calls *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/token/workspace/provision-tenant/":
				fmt.Fprint(w, `{}`)
			case "/api/token/workspace/":
				if err := json.NewEncoder(w).Encode(map[string]any{"access": access, "expires_in": 60}); err != nil {
					t.Error(err)
				}
			case "/api/platform/v1/context/", "/api/my/users/enrollments/":
				if request.Header.Get("Authorization") != "Bearer "+access {
					t.Error("upstream did not receive the issued user token")
				}
				fmt.Fprint(w, `{}`)
			default:
				t.Errorf("unexpected upstream path: %s", request.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}
	crm, lms := upstream(&crmCalls), upstream(&lmsCalls)
	defer crm.Close()
	defer lms.Close()
	t.Setenv("GROWY_PUBLIC_URL", crm.URL)
	t.Setenv("GROWY_API_INTERNAL_URL", crm.URL)
	t.Setenv("GROWY_LMS_PUBLIC_URL", lms.URL)
	t.Setenv("GROWY_LMS_INTERNAL_URL", "")
	h := NewOrganizationHandler(service.NewOrganizationService(repository.NewOrganizationRepository(db, zap.NewNop()), zap.NewNop()), zap.NewNop())
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("claims", &auth.Claims{UserID: 2, Email: "director@example.test", TokenID: "module-test"})
	})
	router.Any("/growy/api/*path", h.ProxyGrowyAPI)
	router.POST("/handoff", h.CreateGrowyHandoff)
	router.POST("/access-check", h.CheckGrowyAccess)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		return recorder
	}
	set := func(app string, enabled bool) {
		_, err := db.Exec(ctx, `UPDATE organization_modules SET enabled=$2 WHERE application=$1`, app, enabled)
		require.NoError(t, err)
	}
	checkBody := fmt.Sprintf(`{"user_id":2,"organization_id":%q}`, org.String())
	set("growy", false)
	require.Equal(t, http.StatusOK, request(http.MethodGet, "/growy/api/context/", "").Code)
	require.Equal(t, int64(3), crmCalls.Load())
	require.JSONEq(t, `{"active":true,"growy_role":"tenant_admin","growy_roles":["tenant_admin"]}`, request(http.MethodPost, "/access-check", checkBody).Body.String())
	foreign := fmt.Sprintf(`{"user_id":2,"organization_id":%q}`, uuid.NewString())
	require.JSONEq(t, `{"active":false}`, request(http.MethodPost, "/access-check", foreign).Body.String())
	require.Equal(t, http.StatusForbidden, request(http.MethodPost, "/handoff", "{}").Code)
	require.Equal(t, http.StatusForbidden, request(http.MethodGet, "/growy/api/users/enrollments/", "").Code)
	require.Zero(t, lmsCalls.Load())
	set("crm", false)
	set("growy", true)
	for _, path := range []string{"context/", "crm/leads/", "whatsapp/integration-status/", "telegram/accounts/1/status/", "integrations/meta/status/"} {
		require.Equal(t, http.StatusForbidden, request(http.MethodGet, "/growy/api/"+path, "").Code, path)
	}
	require.JSONEq(t, `{"active":false}`, request(http.MethodPost, "/access-check", checkBody).Body.String())
	require.Equal(t, int64(3), crmCalls.Load(), "revocation must block requests even with a cached CRM session")
	require.Equal(t, http.StatusOK, request(http.MethodPost, "/handoff", "{}").Code)
	require.Equal(t, http.StatusOK, request(http.MethodGet, "/growy/api/users/enrollments/", "").Code)
	require.Equal(t, int64(4), lmsCalls.Load())
	require.Equal(t, int64(3), crmCalls.Load(), "learning handoff must provision only the LMS")
}

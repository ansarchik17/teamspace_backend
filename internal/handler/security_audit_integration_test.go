package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestSecurityAuditHTTPExportAndAuthorization(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "security_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	_, err = db.Exec(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY,role TEXT,is_active BOOLEAN,organization_id UUID,deleted_at TIMESTAMPTZ);
 CREATE TABLE organizations(id UUID PRIMARY KEY,owner_id BIGINT,status TEXT,updated_at TIMESTAMPTZ DEFAULT now());
 INSERT INTO users(id,role,is_active) VALUES(1,'admin',true),(2,'user',true),(3,'user',true);`)
	require.NoError(t, err)
	org := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,owner_id,status) VALUES($1,2,'active')`, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id IN(2,3)`, org)
	require.NoError(t, err)
	for _, file := range []string{"20261012000100_company_administration_audit.sql", "20261012000500_security_audit.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	repo := repository.NewOrganizationRepository(db, zap.NewNop())
	h := NewOrganizationHandler(service.NewOrganizationService(repo, zap.NewNop()), zap.NewNop())
	actor, ip := int64(3), "192.0.2.3"
	require.NoError(t, repo.RecordSecurityEvent(ctx, domain.SecurityAuditEvent{ActorID: &actor, IP: &ip, Action: "file.files.create", Outcome: "success", RequestID: "=SUM(1,1)", Method: "POST", Route: "/api/v1/drive/files", Status: 201}))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.SecurityAudit(h, zap.NewNop(), nil), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.GetHeader("X-Test-Actor"), 10, 64)
		c.Set("user_id", id)
	})
	r.GET("/api/v1/organizations/current/security-audit", h.SecurityAudit)
	r.GET("/api/v1/organizations/current/security-audit/export", h.ExportSecurityAudit)
	r.GET("/api/v1/platform/security-audit", h.SecurityAudit)
	r.GET("/api/v1/platform/security-audit/export", h.ExportSecurityAudit)
	request := func(actor int64, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "192.0.2.8:4000"
		req.Header.Set("X-Test-Actor", strconv.FormatInt(actor, 10))
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	denied := request(3, "/api/v1/organizations/current/security-audit")
	require.Equal(t, 403, denied.Code)
	denied = request(2, "/api/v1/platform/security-audit/export")
	require.Equal(t, 403, denied.Code)
	csv := request(2, "/api/v1/organizations/current/security-audit/export?action=file.files.create")
	require.Equal(t, 200, csv.Code)
	require.Equal(t, "no-store", csv.Header().Get("Cache-Control"))
	require.Contains(t, csv.Header().Get("Content-Disposition"), "security-audit.csv")
	require.True(t, strings.HasPrefix(csv.Body.String(), "\ufeff"))
	require.Contains(t, csv.Body.String(), "'=SUM")
	jsonResponse := request(1, "/api/v1/platform/security-audit/export?action=file.files.create&format=json")
	require.Equal(t, 200, jsonResponse.Code)
	var page domain.SecurityAuditPage
	require.NoError(t, json.Unmarshal(jsonResponse.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Nil(t, page.NextBeforeID)
	exported := request(2, "/api/v1/organizations/current/security-audit?action=audit.export")
	require.Equal(t, 200, exported.Code)
	require.NoError(t, json.Unmarshal(exported.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, int64(2), *page.Items[0].ActorID)
	require.Equal(t, 400, request(2, "/api/v1/organizations/current/security-audit?ip=invalid").Code)
	require.Equal(t, 400, request(2, "/api/v1/organizations/current/security-audit/export?format=xml").Code)
	_, err = db.Exec(ctx, `INSERT INTO security_audit_events(organization_id,action,outcome) SELECT $1,'file.files.create','success' FROM generate_series(1,10001)`, org)
	require.NoError(t, err)
	oversized := request(2, "/api/v1/organizations/current/security-audit/export?action=file.files.create")
	require.Equal(t, 409, oversized.Code)
	require.Empty(t, oversized.Header().Get("Content-Disposition"))
	require.NotContains(t, oversized.Body.String(), "id,created_at")
}

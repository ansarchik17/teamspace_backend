package handler

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestAIAdministrationHTTP(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "ai_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,owner_id BIGINT,status TEXT NOT NULL DEFAULT 'active',updated_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),role TEXT NOT NULL DEFAULT 'user',is_active BOOLEAN NOT NULL DEFAULT true,deleted_at TIMESTAMPTZ,full_name_local TEXT,first_name TEXT,last_name TEXT,username TEXT,email TEXT);`)
	require.NoError(t, err)
	for _, file := range []string{"20261012000100_company_administration_audit.sql", "20261012000200_company_user_blocks.sql", "20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql", "20261022100200_ai_personal_weekly_windows.sql", "20261020100900_ai_enforcement_mode_unconfigured.sql", "20261022100400_ai_personal_access_policy.sql", "20261022100500_ai_final_request_cutoffs.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, e)
	}
	a, b := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,owner_id) VALUES($1,2),($2,4)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id,role,username) VALUES(1,NULL,'admin','admin'),(2,$1,'user','director'),(3,$1,'user','employee'),(4,$2,'user','other')`, a, b)
	require.NoError(t, err)
	h := NewOrganizationHandler(service.NewOrganizationService(repository.NewOrganizationRepository(db, zap.NewNop()), zap.NewNop()), zap.NewNop())
	platform, director, employee := aiTestRouter(h, 1), aiTestRouter(h, 2), aiTestRouter(h, 3)
	personalPath := "/api/v1/platform/ai/users/1/quota"
	personal := aiTestRequest(platform, "GET", personalPath, "", "ru")
	require.Equal(t, 200, personal.Code)
	require.Contains(t, personal.Body.String(), `"weekly_quota_microcredits":"100000000000"`)
	require.Contains(t, personal.Body.String(), `"five_hour_limit_microcredits":"20000000000"`)
	require.Equal(t, 403, aiTestRequest(employee, "GET", personalPath, "", "ru").Code)
	personalBody := `{"weekly_quota_microcredits":"1000000000","five_hour_limit_microcredits":"200000000","revision":"0"}`
	require.Equal(t, 403, aiTestRequest(director, "PUT", personalPath, personalBody, "ru").Code)
	require.Equal(t, 200, aiTestRequest(platform, "PUT", personalPath, personalBody, "ru").Code)
	require.Equal(t, 409, aiTestRequest(platform, "PUT", personalPath, personalBody, "ru").Code)
	current := "/api/v1/organizations/current/administration/ai-budget"
	selected := "/api/v1/platform/organizations/" + a.String() + "/administration/ai-budget"
	w := aiTestRequest(director, "GET", current, "", "ru")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"configured":false`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	body := `{"weekly_budget_microcredits":"1000","max_five_hour_microcredits":"100","service_weekly_quota_microcredits":"0","revision":"0"}`
	w = aiTestRequest(director, "PUT", selected, body, "ru")
	require.Equal(t, 403, w.Code)
	require.Equal(t, "FORBIDDEN", aiErrorCode(t, w))
	w = aiTestRequest(platform, "PUT", selected, body, "ru")
	require.Equal(t, 204, w.Code)
	require.Empty(t, w.Body.String())
	w = aiTestRequest(platform, "PUT", selected, body, "kk")
	require.Equal(t, 409, w.Code)
	require.Equal(t, "AI_REVISION_CONFLICT", aiErrorCode(t, w))
	require.Equal(t, "kk", w.Header().Get("Content-Language"))
	kkMessage := w.Body.String()
	ru := aiTestRequest(platform, "PUT", selected, body, "ru")
	require.NotEqual(t, kkMessage, ru.Body.String())
	require.Contains(t, ru.Body.String(), `"current_revision":"1"`)
	quota := `{"weekly_quota_microcredits":"600","five_hour_limit_microcredits":"100","revision":"0","company_revision":"1"}`
	w = aiTestRequest(employee, "PUT", current+"/users/3", quota, "ru")
	require.Equal(t, 403, w.Code)
	w = aiTestRequest(director, "PUT", current+"/users/4", quota, "ru")
	require.Equal(t, 404, w.Code)
	w = aiTestRequest(director, "PUT", current+"/users/3", quota, "ru")
	require.Equal(t, 204, w.Code)
	w = aiTestRequest(director, "PUT", current+"/users/3", quota, "ru")
	require.Equal(t, 409, w.Code)
	require.Equal(t, "AI_REVISION_CONFLICT", aiErrorCode(t, w))
	require.Contains(t, w.Body.String(), `"scope":"user"`)
	wrongCompany := strings.Replace(quota, `"company_revision":"1"`, `"company_revision":"0"`, 1)
	w = aiTestRequest(director, "PUT", current+"/users/3", wrongCompany, "ru")
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), `"scope":"user"`)
	for _, path := range []string{current + "/users?limit=1&offset=1", current + "/users/3", selected + "/users/3"} {
		router := director
		if strings.HasPrefix(path, selected) {
			router = platform
		}
		w = aiTestRequest(router, "GET", path, "", "ru")
		require.Equal(t, 200, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.Contains(t, w.Body.String(), `"weekly_quota_microcredits":"600"`)
		require.Contains(t, w.Body.String(), `"company_revision":"1"`)
	}
	w = aiTestRequest(director, "GET", current+"/users/4", "", "ru")
	require.Equal(t, 404, w.Code)
	w = aiTestRequest(employee, "GET", current+"/users", "", "ru")
	require.Equal(t, 403, w.Code)
	w = aiTestRequest(platform, "GET", selected, "", "ru")
	require.Equal(t, 200, w.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "400", response["unallocated_microcredits"])
	require.Equal(t, "1000", response["available_microcredits"])
	require.Equal(t, "enforced", response["enforcement_mode"])
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)
	w = aiTestRequest(platform, "PUT", selected+"/users/3", strings.Replace(quota, `"revision":"0"`, `"revision":"1"`, 1), "kk")
	require.Equal(t, 409, w.Code)
	require.Equal(t, "AI_COMPANY_SUSPENDED", aiErrorCode(t, w))
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=1`)
	require.NoError(t, err)
	w = aiTestRequest(platform, "GET", selected, "", "ru")
	require.Equal(t, 403, w.Code)
}

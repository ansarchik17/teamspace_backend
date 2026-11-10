package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func newAIQuotaWriteDB(t *testing.T) (context.Context, *pgxpool.Pool, *OrganizationRepository, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	for _, file := range []string{"20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql", "20261022100200_ai_personal_weekly_windows.sql", "20261020100900_ai_enforcement_mode_unconfigured.sql", "20261022100400_ai_personal_access_policy.sql", "20261022100500_ai_final_request_cutoffs.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	return ctx, db, NewOrganizationRepository(db, zap.NewNop()), a, b
}

func aiUserQuotaRequest(q, h, revision, companyRevision int64) domain.SetAIUserQuotaRequest {
	return domain.SetAIUserQuotaRequest{WeeklyQuota: &q, FiveHourLimit: &h, Revision: &revision, CompanyRevision: &companyRevision}
}

func requireAIQuotaError(t *testing.T, err error, code apperrors.ErrorCode) *apperrors.AppError {
	t.Helper()
	var app *apperrors.AppError
	require.ErrorAs(t, err, &app)
	require.Equal(t, code, app.Code)
	return app
}

func TestAIUserQuotaConcurrentAllocation(t *testing.T) {
	ctx, db, r, a, _ := newAIQuotaWriteDB(t)
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(600, 100, 0, 0)))
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, target := range []int64{2, 3} {
		wg.Add(1)
		go func(target int64) {
			defer wg.Done()
			<-start
			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			results <- r.SetAdministrationAIUserQuota(callCtx, 2, target, nil, aiUserQuotaRequest(400, 100, 0, 1))
		}(target)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireAIQuotaError(t, err, apperrors.ErrCodeAIBudgetConflict)
		}
	}
	require.Equal(t, 2, success)
	var allocated, revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT SUM(weekly_quota_microcredits) FROM organization_ai_user_quotas WHERE organization_id=$1`, a).Scan(&allocated))
	require.Equal(t, int64(800), allocated)
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_ai_budgets WHERE organization_id=$1`, a).Scan(&revision))
	require.Equal(t, int64(1), revision)
	var auditCount int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE action='ai.user_quota.update'`).Scan(&auditCount))
	require.Equal(t, 2, auditCount)
}

func TestSetAdministrationAIUserQuota(t *testing.T) {
	ctx, db, r, a, b := newAIQuotaWriteDB(t)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 3, 3, nil, aiUserQuotaRequest(100, 50, 0, 0)), apperrors.ErrCodeForbidden)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 2, 5, nil, aiUserQuotaRequest(100, 50, 0, 0)), apperrors.ErrCodeNotFound)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, &a, aiUserQuotaRequest(100, 50, 0, 0)), apperrors.ErrCodeForbidden)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 1, 0, &a, aiUserQuotaRequest(100, 50, 0, 0)), apperrors.ErrCodeValidation)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(50, 100, 0, 0)), apperrors.ErrCodeValidation)
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 50, 0, 0)), "no company budget required")
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 50, 1, 999)), "company revision no longer affects personal quota")
	conflict := requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(200, 100, 0, 0)), apperrors.ErrCodeAIRevisionConflict)
	require.Equal(t, "user", conflict.Details["scope"])
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 1, 3, &a, aiUserQuotaRequest(1000, 500, 1, 0)))
	var rev int64
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_ai_user_quotas WHERE user_id=3`).Scan(&rev))
	require.EqualValues(t, 2, rev)
	_, err := db.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT reject_ai_quota_audit CHECK(action <> 'ai.user_quota.update') NOT VALID`)
	require.NoError(t, err)
	require.Error(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(10, 5, 2, 0)))
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_ai_user_quotas WHERE user_id=3`).Scan(&rev))
	require.EqualValues(t, 2, rev)
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit DROP CONSTRAINT reject_ai_quota_audit`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 50, 2, 0)), apperrors.ErrCodeNotFound)
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)
	requireAIQuotaError(t, r.SetAdministrationAIUserQuota(ctx, 1, 2, &a, aiUserQuotaRequest(100, 50, 0, 0)), apperrors.ErrCodeAICompanySuspended)
}

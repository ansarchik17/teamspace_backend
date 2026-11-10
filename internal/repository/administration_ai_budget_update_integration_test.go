package repository

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func aiBudgetRequest(budget, five, service, revision int64) domain.SetAIBudgetRequest {
	return domain.SetAIBudgetRequest{WeeklyBudget: &budget, MaxFiveHour: &five, ServiceQuota: &service, Revision: &revision}
}

func TestSetAdministrationAIBudget(t *testing.T) {
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	for _, file := range []string{"20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql", "20261022100200_ai_personal_weekly_windows.sql", "20261020100900_ai_enforcement_mode_unconfigured.sql", "20261022100400_ai_personal_access_policy.sql", "20261022100500_ai_final_request_cutoffs.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	r := NewOrganizationRepository(db, zap.NewNop())
	assertCode := func(err error, code apperrors.ErrorCode) {
		t.Helper()
		var app *apperrors.AppError
		require.ErrorAs(t, err, &app)
		require.Equal(t, code, app.Code)
	}
	assertCode(r.SetAdministrationAIBudget(ctx, 2, &a, aiBudgetRequest(1000, 100, 100, 0)), apperrors.ErrCodeForbidden)
	assertCode(r.SetAdministrationAIBudget(ctx, 1, nil, aiBudgetRequest(1000, 100, 100, 0)), apperrors.ErrCodeForbidden)
	assertCode(r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(-1, 100, 0, 0)), apperrors.ErrCodeValidation)
	assertCode(r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 100, 1)), apperrors.ErrCodeAIRevisionConflict)
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 100, 0)))
	var revision, budget int64
	var audits int
	read := func() {
		t.Helper()
		require.NoError(t, db.QueryRow(ctx, `SELECT revision,weekly_budget_microcredits FROM organization_ai_budgets WHERE organization_id=$1`, a).Scan(&revision, &budget))
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE action='ai.budget.update'`).Scan(&audits))
	}
	read()
	require.Equal(t, int64(1), revision)
	require.Equal(t, 1, audits)
	var before, after json.RawMessage
	require.NoError(t, db.QueryRow(ctx, `SELECT before_value,after_value FROM organization_admin_audit WHERE action='ai.budget.update'`).Scan(&before, &after))
	require.JSONEq(t, `null`, string(before))
	require.JSONEq(t, `{"weekly_budget_microcredits":"1000","max_five_hour_microcredits":"100","service_weekly_quota_microcredits":"0","revision":"1"}`, string(after))
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 100, 1)))
	read()
	require.Equal(t, int64(1), revision)
	require.Equal(t, 1, audits, "no-op has no audit")
	assertCode(r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 100, 0)), apperrors.ErrCodeAIRevisionConflict)
	_, err := db.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,2,200,100),($1,3,600,100)`, a)
	require.NoError(t, err)
	assertCode(r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(799, 100, 100, 1)), apperrors.ErrCodeAIBudgetConflict)
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 201, 1)), "legacy allocation is ignored")
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(900, 50, 100, 1)))
	read()
	require.Equal(t, int64(2), revision)
	require.Equal(t, 2, audits)
	var personalH int64
	require.NoError(t, db.QueryRow(ctx, `SELECT five_hour_limit_microcredits FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=3`, a).Scan(&personalH))
	require.Equal(t, int64(100), personalH, "Hmax decrease must not rewrite H")
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT reject_ai_budget_audit CHECK(action <> 'ai.budget.update') NOT VALID`)
	require.NoError(t, err)
	require.Error(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1100, 100, 100, 2)))
	read()
	require.Equal(t, int64(2), revision)
	require.Equal(t, int64(900), budget)
	require.Equal(t, 2, audits)
	require.Error(t, r.SetAdministrationAIBudget(ctx, 1, &b, aiBudgetRequest(1000, 100, 0, 0)))
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_budgets WHERE organization_id=$1`, b).Scan(&count))
	require.Zero(t, count, "audit failure rolls back initial creation")
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit DROP CONSTRAINT reject_ai_budget_audit`)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, amount := range []int64{1001, 1002} {
		wg.Add(1)
		go func(amount int64) {
			defer wg.Done()
			results <- r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(amount, 100, 100, 2))
		}(amount)
	}
	wg.Wait()
	close(results)
	success := 0
	for result := range results {
		if result == nil {
			success++
		} else {
			assertCode(result, apperrors.ErrCodeAIRevisionConflict)
		}
	}
	require.Equal(t, 1, success)
	read()
	require.Equal(t, int64(3), revision)
	require.Equal(t, 3, audits)
	// Confirmed service consumption and unresolved holds constrain the budget
	// even when the nominal service allocation is lower.
	anchor := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	period, op, attempt := uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_weekly_anchors VALUES($1,$2)`, a, anchor)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_weekly_periods(id,organization_id,anchor_at,starts_at,ends_at,used_microcredits) VALUES($1,$2,$3,$3,$4,200)`, period, a, anchor, anchor.Add(168*time.Hour))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_service_weekly_usage(organization_id,period_id,used_microcredits) VALUES($1,$2,200)`, a, period)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_operations(id,organization_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,$2,'service','test','budget-hold',decode(repeat('ab',32),'hex'))`, op, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,company_period_id) VALUES($1,$2,$3,1,'nitec','chat','model','credits-v1','test',1000,$4)`, attempt, op, a, period)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,50)`, attempt)
	require.NoError(t, err)
	assertCode(r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 100, 3)), apperrors.ErrCodeAIBudgetConflict)
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1200, 100, 100, 3)), "platform can configure suspended company")
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1200, 100, 101, 4)))
	require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(0, 100, 100, 4)))
	read()
	require.Zero(t, budget)
	require.Equal(t, int64(5), revision)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=1`)
	require.NoError(t, err)
	assertCode(r.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 100, 5)), apperrors.ErrCodeForbidden)
}

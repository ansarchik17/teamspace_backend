package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func TestAIEmployeeLifecycle(t *testing.T) {
	ctx, db, r, a, b := newAIQuotaWriteDB(t)
	migration, err := os.ReadFile("../../db/migrations/20261020100400_ai_employee_lifecycle.sql")
	require.NoError(t, err)
	block, err := os.ReadFile("../../db/migrations/20261012000200_company_user_blocks.sql")
	require.NoError(t, err)
	for _, data := range [][]byte{block, migration} {
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	for _, org := range []uuid.UUID{a, b} {
		require.NoError(t, r.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(1000, 100, 0, 0)))
	}
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id,is_active) VALUES(7,$1,true)`, a)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_user_quotas WHERE user_id=7`).Scan(&count))
	require.Zero(t, count, "joining does not grant a quota")
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(600, 100, 0, 1)))
	user := int64(3)
	clock := time.Now().UTC().Truncate(time.Microsecond)
	periods := NewAIPeriodRepository(func() time.Time { return clock })
	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	windows, err := periods.OpenForReservation(ctx, tx, a, &user)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_user_weekly_usage(organization_id,period_id,user_id,used_microcredits) VALUES($1,$2,3,80)`, a, windows.Weekly.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_ai_weekly_periods SET used_microcredits=80 WHERE id=$1`, windows.Weekly.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE user_ai_five_hour_periods SET used_microcredits=80 WHERE id=$1`, windows.FiveHour.ID)
	require.NoError(t, err)
	op, attempt := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_operations(id,organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,$2,3,'user','test','lifecycle',decode(repeat('ab',32),'hex'))`, op, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,company_period_id,five_hour_period_id) VALUES($1,$2,$3,1,'test','chat','model','credits-v1','test',1000,$4,$5)`, attempt, op, a, windows.Weekly.ID, windows.FiveHour.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,20)`, attempt)
	require.NoError(t, err)
	assertQuota := func(org uuid.UUID, revision int64, assigned bool) {
		t.Helper()
		var q *int64
		var actual int64
		require.NoError(t, db.QueryRow(ctx, `SELECT weekly_quota_microcredits,revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=3`, org).Scan(&q, &actual))
		require.Equal(t, revision, actual)
		require.Equal(t, assigned, q != nil)
	}
	require.NoError(t, r.SetEmployeeBlocked(ctx, 2, 3, nil, true))
	assertQuota(a, 1, true)
	// Authentication sync cannot reactivate an administratively blocked user.
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=3`)
	require.NoError(t, err)
	tx, err = db.Begin(ctx)
	require.NoError(t, err)
	_, err = periods.OpenForReservation(ctx, tx, a, &user)
	require.ErrorIs(t, err, ErrAIPeriodMembership)
	require.NoError(t, tx.Rollback(ctx))
	require.NoError(t, r.SetEmployeeBlocked(ctx, 2, 3, nil, false))
	assertQuota(a, 1, true)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	assertQuota(a, 2, false)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=3`, b).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 4, 3, nil, aiUserQuotaRequest(600, 100, 0, 1)))
	tx, err = db.Begin(ctx)
	require.NoError(t, err)
	other, err := periods.OpenForReservation(ctx, tx, b, &user)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	require.Equal(t, windows.FiveHour.ID, other.FiveHour.ID, "transfer reuses the global five-hour window")
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, a)
	require.NoError(t, err)
	assertQuota(a, 2, false)
	assertQuota(b, 2, false)
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(600, 100, 2, 1)))
	for i := 0; i < 2; i++ {
		_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now(),is_active=false WHERE id=3`)
		require.NoError(t, err)
		_, err = db.Exec(ctx, `UPDATE users SET deleted_at=NULL,is_active=true WHERE id=3`)
		require.NoError(t, err)
	}
	assertQuota(a, 4, false)
	require.NoError(t, r.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 100, 4, 1)))
	summary, err := r.AdministrationAIBudget(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, int64(80), summary.Used)
	require.Equal(t, int64(20), summary.Reserved)
	require.Equal(t, int64(100), summary.Committed)
	var used, reserved int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, windows.FiveHour.ID).Scan(&used))
	require.Equal(t, int64(80), used)
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM ai_usage_reservations WHERE attempt_id=$1 AND status='active'`, attempt).Scan(&reserved))
	require.Equal(t, int64(20), reserved)
	var source uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `SELECT organization_id FROM ai_usage_operations WHERE id=$1`, op).Scan(&source))
	require.Equal(t, a, source)

	t.Run("concurrent block and quota", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan error, 2)
		start := make(chan struct{})
		for _, blocking := range []bool{true, false} {
			wg.Add(1)
			go func(blocking bool) {
				defer wg.Done()
				<-start
				call, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				if blocking {
					results <- r.SetEmployeeBlocked(call, 2, 3, nil, true)
				} else {
					results <- r.SetAdministrationAIUserQuota(call, 2, 3, nil, aiUserQuotaRequest(200, 100, 5, 1))
				}
			}(blocking)
		}
		close(start)
		wg.Wait()
		close(results)
		for result := range results {
			require.NoError(t, result)
		}
		assertQuota(a, 6, true)
	})
	t.Run("concurrent transfer and quota", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan error, 2)
		start := make(chan struct{})
		for _, transfer := range []bool{true, false} {
			wg.Add(1)
			go func(transfer bool) {
				defer wg.Done()
				<-start
				call, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				if transfer {
					_, e := db.Exec(call, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
					results <- e
				} else {
					results <- r.SetAdministrationAIUserQuota(call, 2, 3, nil, aiUserQuotaRequest(300, 100, 6, 1))
				}
			}(transfer)
		}
		close(start)
		wg.Wait()
		close(results)
		for result := range results {
			if result != nil {
				requireAIQuotaError(t, result, apperrors.ErrCodeNotFound)
			}
		}
		var assigned bool
		require.NoError(t, db.QueryRow(ctx, `SELECT weekly_quota_microcredits IS NOT NULL FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=3`, a).Scan(&assigned))
		require.False(t, assigned, "a racing assignment cannot survive departure")
		assertQuota(b, 2, false)
	})
	// Rollback migration leaves existing histories and settings intact.
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[1])
	require.NoError(t, err)
	assertQuota(b, 2, false)
}

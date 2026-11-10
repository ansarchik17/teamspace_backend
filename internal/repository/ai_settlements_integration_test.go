package repository

import (
	"context"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func newAISettlementTestDB(t *testing.T) (context.Context, *pgxpool.Pool, *AIReservationRepository, *OrganizationRepository, uuid.UUID, uuid.UUID, *time.Time) {
	t.Helper()
	ctx, db, r, admin, a, b, now := newAIReserveTestDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100500_ai_usage_settlements.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	data, err = os.ReadFile("../../db/migrations/20261020100600_ai_failure_outcomes.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(100_000, 100_000, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100_000, 100_000, 0, 1)))
	return ctx, db, r, admin, a, b, now
}

func aiTokenCount(v int64) *int64 { return &v }

func aiSettleRequest(org uuid.UUID, attempt uuid.UUID, input, output int64) domain.AISettleRequest {
	return domain.AISettleRequest{OrganizationID: org, AttemptID: attempt, ActualModel: "openai/gpt-oss-120b", UsageAdapterVersion: "test-v1", UsageSource: "provider_usage", Usage: domain.AICreditUsage{Status: "verified", InputTokens: aiTokenCount(input), OutputTokens: aiTokenCount(output), TotalTokens: aiTokenCount(input + output)}, ReportedInputTokens: aiTokenCount(input), ReportedOutputTokens: aiTokenCount(output), ReportedTotalTokens: aiTokenCount(input + output)}
}

func TestAISettleSuccessAndOverrun(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	reserved, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "settle", 10_000))
	require.NoError(t, err)
	req := aiSettleRequest(a, reserved.AttemptID, 2, 1)
	req.Usage.CachedInput = aiTokenCount(1)
	req.Usage.ReasoningOutput = aiTokenCount(1)
	response := "provider-response-1"
	req.ProviderRequestID = &response
	settled, err := r.SettleSuccess(ctx, req)
	require.NoError(t, err)
	require.True(t, settled.Applied)
	require.Equal(t, int64(5000), settled.ChargedMicrocredits)
	require.Equal(t, int64(5000), settled.ReleasedMicrocredits)
	require.Zero(t, settled.OverrunMicrocredits)
	require.Equal(t, "completed", settled.OperationStatus)
	var used, held int64
	for _, query := range []string{
		`SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`,
		`SELECT used_microcredits,reserved_microcredits FROM organization_ai_user_weekly_usage WHERE period_id=$1`,
	} {
		require.NoError(t, db.QueryRow(ctx, query, reserved.CompanyPeriodID).Scan(&used, &held))
		require.Equal(t, int64(5000), used)
		require.Zero(t, held)
	}
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, reserved.FiveHourPeriodID).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Zero(t, held)
	var input, output, total, cached, reasoning int64
	var actual, usageStatus, reservationStatus string
	require.NoError(t, db.QueryRow(ctx, `SELECT a.input_tokens,a.output_tokens,a.total_tokens,a.cached_input_tokens,a.reasoning_output_tokens,a.actual_model,a.usage_status,r.status FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1`, reserved.AttemptID).Scan(&input, &output, &total, &cached, &reasoning, &actual, &usageStatus, &reservationStatus))
	require.Equal(t, []int64{2, 1, 3, 1, 1}, []int64{input, output, total, cached, reasoning})
	require.Equal(t, req.ActualModel, actual)
	require.Equal(t, "verified", usageStatus)
	require.Equal(t, "settled", reservationStatus)
	replay, err := r.SettleSuccess(ctx, req)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	require.Equal(t, settled.ChargedMicrocredits, replay.ChargedMicrocredits)
	changed := req
	changed.Usage.InputTokens = aiTokenCount(3)
	changed.Usage.TotalTokens = aiTokenCount(4)
	_, err = r.SettleSuccess(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAISettlementConflict)
	// A duplicate response ID cannot be charged against a different attempt.
	next, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "overrun", 95_000))
	require.NoError(t, err)
	duplicate := aiSettleRequest(a, next.AttemptID, 100, 0)
	duplicate.ProviderRequestID = &response
	_, err = r.SettleSuccess(ctx, duplicate)
	require.ErrorIs(t, err, domain.ErrAISettlementConflict)
	overrun, err := r.SettleSuccess(ctx, aiSettleRequest(a, next.AttemptID, 100, 0))
	require.NoError(t, err)
	require.Equal(t, int64(100_000), overrun.ChargedMicrocredits)
	require.Equal(t, int64(5000), overrun.OverrunMicrocredits)
	require.Zero(t, overrun.ReleasedMicrocredits)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, reserved.CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(105_000), used, "record full confirmed cost above budget")
	require.Zero(t, held)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "after-overrun", 1))
	requireReserveDenied(t, err, "USER_WEEKLY_EXHAUSTED")
	_, err = db.Exec(ctx, `UPDATE ai_usage_settlements SET overrun_microcredits=0 WHERE attempt_id=$1`, next.AttemptID)
	require.Error(t, err, "final evidence is immutable")
}

func TestAISettleConcurrentAndReserve(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	reserved, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "parallel-settle", 10_000))
	require.NoError(t, err)
	req := aiSettleRequest(a, reserved.AttemptID, 2, 1)
	results := make(chan error, 17)
	applied := make(chan bool, 16)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 17; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			call, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if i == 16 {
				_, err := r.Reserve(call, aiReserveRequest(a, &uid, "parallel-reserve", 90_000))
				results <- err
				return
			}
			out, err := r.SettleSuccess(call, req)
			applied <- out.Applied
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(applied)
	for err := range results {
		require.NoError(t, err)
	}
	count := 0
	for v := range applied {
		if v {
			count++
		}
	}
	require.Equal(t, 1, count)
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, reserved.CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Equal(t, int64(90_000), held)
}

func TestAISettleAfterTransferAndPeriods(t *testing.T) {
	ctx, db, r, admin, a, b, now := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "original-company", 10_000))
	require.NoError(t, err)
	*now = now.Add(168 * time.Hour)
	// Materialize a later company and personal window containing the old hold.
	later, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "later-period", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &b, aiBudgetRequest(100_000, 100_000, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 4, 3, nil, aiUserQuotaRequest(100_000, 100_000, 0, 1)))
	moved, err := r.Reserve(ctx, aiReserveRequest(b, &uid, "new-company", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now(),is_active=false WHERE id=3`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)
	out, err := r.SettleSuccess(ctx, aiSettleRequest(a, first.AttemptID, 2, 1))
	require.NoError(t, err, "historical billing must work for dismissed users/suspended companies")
	require.True(t, out.Applied)
	var used, held, carried int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits,carried_reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held, &carried))
	require.Equal(t, int64(5000), used)
	require.Zero(t, held)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits,carried_reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, later.CompanyPeriodID).Scan(&used, &held, &carried))
	require.Zero(t, used)
	require.Equal(t, int64(10_000), held)
	require.Zero(t, carried)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits,carried_reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, moved.FiveHourPeriodID).Scan(&used, &held, &carried))
	require.Zero(t, used)
	require.Equal(t, int64(20_000), held)
	require.Zero(t, carried)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, moved.CompanyPeriodID).Scan(&used, &held))
	require.Zero(t, used)
	require.Equal(t, int64(10_000), held)
}

func TestAISettleModelsZeroAndInvalidUsage(t *testing.T) {
	ctx, db, r, _, a, b, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "models", 10_000))
	require.NoError(t, err)
	req := aiSettleRequest(a, first.AttemptID, 2, 1)
	foreign := req
	foreign.OrganizationID = b
	_, err = r.SettleSuccess(ctx, foreign)
	require.ErrorIs(t, err, domain.ErrAISettlementNotFound)
	invalid := req
	invalid.Usage.InputTokens = nil
	_, err = r.SettleSuccess(ctx, invalid)
	require.ErrorIs(t, err, domain.ErrAICreditUnknownUsage)
	invalid = req
	invalid.Usage.TotalTokens = aiTokenCount(99)
	_, err = r.SettleSuccess(ctx, invalid)
	require.ErrorIs(t, err, domain.ErrAICreditInvalidUsage)
	invalid = req
	invalid.Usage.InputTokens = aiTokenCount(math.MaxInt64)
	_, err = r.SettleSuccess(ctx, invalid)
	require.ErrorIs(t, err, domain.ErrAICreditOverflow)
	invalid = req
	invalid.ActualModel = "unknown"
	_, err = r.SettleSuccess(ctx, invalid)
	require.ErrorIs(t, err, domain.ErrAICreditUnknownModel)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_reservations WHERE attempt_id=$1`, first.AttemptID).Scan(&status))
	require.Equal(t, "active", status)
	req.ActualModel = "zai-org/GLM-4.6"
	actual, err := r.SettleSuccess(ctx, req)
	require.NoError(t, err)
	require.Equal(t, int64(10_000), actual.ChargedMicrocredits)
	var originalWeight, actualWeight int64
	require.NoError(t, db.QueryRow(ctx, `SELECT a.input_weight_microcredits,s.input_weight_microcredits FROM ai_usage_attempts a JOIN ai_usage_settlements s ON s.attempt_id=a.id WHERE a.id=$1`, first.AttemptID).Scan(&originalWeight, &actualWeight))
	require.Equal(t, int64(1000), originalWeight)
	require.Equal(t, int64(2000), actualWeight)
	second, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "zero", 10_000))
	require.NoError(t, err)
	zero, err := r.SettleSuccess(ctx, aiSettleRequest(a, second.AttemptID, 0, 0))
	require.NoError(t, err)
	require.Zero(t, zero.ChargedMicrocredits)
	require.Equal(t, int64(10_000), zero.ReleasedMicrocredits)
}

func TestAISettleRollbackAndService(t *testing.T) {
	ctx, db, r, admin, a, _, _ := newAISettlementTestDB(t)
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(0, 0, 1, 1)))
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(100_000, 100_000, 100_000, 1)))
	first, err := r.Reserve(ctx, aiReserveRequest(a, nil, "service-settle", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE FUNCTION reject_settlement_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test settlement failure'; END $$; CREATE TRIGGER reject_settlement_test BEFORE UPDATE ON organization_ai_service_weekly_usage FOR EACH ROW EXECUTE FUNCTION reject_settlement_test()`)
	require.NoError(t, err)
	req := aiSettleRequest(a, first.AttemptID, 2, 1)
	_, err = r.SettleSuccess(ctx, req)
	require.Error(t, err)
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Zero(t, used)
	require.Equal(t, int64(10_000), held)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_reservations WHERE attempt_id=$1`, first.AttemptID).Scan(&status))
	require.Equal(t, "active", status)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_settlements`).Scan(&count))
	require.Zero(t, count)
	_, err = db.Exec(ctx, `DROP TRIGGER reject_settlement_test ON organization_ai_service_weekly_usage`)
	require.NoError(t, err)
	out, err := r.SettleSuccess(ctx, req)
	require.NoError(t, err)
	require.True(t, out.Applied)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_service_weekly_usage WHERE period_id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Zero(t, held)
}

func TestAISettlePersistedPricesAndUnknownRetry(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "historical-prices", 10_000))
	require.NoError(t, err)
	// Reproduce an old immutable policy snapshot no longer in today's registry.
	// Completion must use this evidence, not today's model price lookup.
	attempt := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,output_weight_microcredits,cached_input_weight_microcredits,reasoning_output_weight_microcredits,company_period_id,five_hour_period_id) VALUES($1,$2,$3,2,'nitec','chat','openai/gpt-oss-120b','retired-policy','test-v1',7,11,7,11,$4,$5)`, attempt, first.OperationID, a, first.CompanyPeriodID, first.FiveHourPeriodID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,1000)`, attempt)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE ai_usage_attempts SET status='unknown' WHERE id=$1`, first.AttemptID)
	require.NoError(t, err)
	out, err := r.SettleSuccess(ctx, aiSettleRequest(a, attempt, 2, 1))
	require.NoError(t, err)
	require.Equal(t, int64(25), out.ChargedMicrocredits)
	require.Equal(t, "needs_reconciliation", out.OperationStatus)
	var held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM ai_usage_reservations WHERE attempt_id=$1 AND status='active'`, first.AttemptID).Scan(&held))
	require.Equal(t, int64(10_000), held, "successful retry must not release an unknown previous attempt")
	last, err := r.SettleSuccess(ctx, aiSettleRequest(a, first.AttemptID, 2, 1))
	require.NoError(t, err)
	require.Equal(t, "completed", last.OperationStatus)
}

func TestAISettleCounterOverflow(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "counter-overflow", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_ai_weekly_periods SET used_microcredits=$2 WHERE id=$1`, first.CompanyPeriodID, int64(math.MaxInt64-10_000))
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(a, first.AttemptID, 20, 0))
	require.ErrorIs(t, err, domain.ErrAICreditOverflow)
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(math.MaxInt64-10_000), used)
	require.Equal(t, int64(10_000), held)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_attempts WHERE id=$1`, first.AttemptID).Scan(&status))
	require.Equal(t, "reserved", status)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_settlements`).Scan(&count))
	require.Zero(t, count)
}

func TestAISettlementMigration(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100500_ai_usage_settlements.sql")
	require.NoError(t, err)
	parts := strings.Split(string(data), "-- +goose Down")
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err, "empty migration can be rolled back")
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "migration-evidence", 10_000))
	require.NoError(t, err)
	_, err = r.SettleSuccess(ctx, aiSettleRequest(a, first.AttemptID, 2, 1))
	require.NoError(t, err)
	_, err = db.Exec(ctx, parts[1])
	require.Error(t, err, "rollback must not silently delete historical result evidence")
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_settlements`).Scan(&count))
	require.Equal(t, 1, count)
}

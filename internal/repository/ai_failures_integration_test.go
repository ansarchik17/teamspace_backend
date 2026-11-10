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
	"github.com/west6ide/workspace-backend/internal/domain"
)

func aiFailureRequest(org, attempt uuid.UUID, outcome domain.AIFailureOutcome, reason string) domain.AIFailureRequest {
	req := domain.AIFailureRequest{OrganizationID: org, AttemptID: attempt, EventID: uuid.New(), Outcome: outcome, Reason: reason, EvidenceSource: "executor"}
	if outcome == domain.AIFailureNotDispatched {
		req.EvidenceSource = "local_dispatch_guard"
	}
	if outcome == domain.AIFailureNotExecuted {
		ref := "provider-confirmation-1"
		req.EvidenceSource = "provider_confirmation"
		req.EvidenceReference = &ref
	}
	return req
}

func TestAIFailureBeforeDispatch(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "local-failure", 10_000))
	require.NoError(t, err)
	req := aiFailureRequest(a, first.AttemptID, domain.AIFailureNotDispatched, "local_error")
	out, err := r.FinishFailure(ctx, req)
	require.NoError(t, err)
	require.True(t, out.Applied)
	require.Equal(t, "rejected", out.AttemptStatus)
	require.Equal(t, "failed", out.OperationStatus)
	require.NotNil(t, out.ChargedMicrocredits)
	require.Zero(t, *out.ChargedMicrocredits)
	require.Equal(t, int64(10_000), out.ReleasedMicrocredits)
	replay, err := r.FinishFailure(ctx, req)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	changed := req
	changed.Reason = "client_cancelled"
	_, err = r.FinishFailure(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAISettlementConflict)
	claimed, err := r.MarkSent(ctx, a, first.AttemptID)
	require.NoError(t, err)
	require.False(t, claimed, "released attempts cannot dispatch")
	var input, output *int64
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT input_tokens,output_tokens FROM ai_usage_attempts WHERE id=$1`, first.AttemptID).Scan(&input, &output))
	require.Nil(t, input, "confirmed non-execution does not invent real token counters")
	require.Nil(t, output)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Zero(t, used)
	require.Zero(t, held)
	_, err = db.Exec(ctx, `DELETE FROM ai_usage_failure_events WHERE event_id=$1`, req.EventID)
	require.Error(t, err, "failure evidence is immutable")
}

func TestAIFailureUnknownAndProviderConfirmation(t *testing.T) {
	for _, reason := range []string{"provider_error", "timeout", "client_cancelled"} {
		t.Run(reason, func(t *testing.T) {
			ctx, db, r, _, a, b, _ := newAISettlementTestDB(t)
			uid := int64(3)
			first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "unknown", 10_000))
			require.NoError(t, err)
			claimed, err := r.MarkSent(ctx, a, first.AttemptID)
			require.NoError(t, err)
			require.True(t, claimed)
			claimed, err = r.MarkSent(ctx, a, first.AttemptID)
			require.NoError(t, err)
			require.False(t, claimed, "a dispatch claim can be acquired only once")
			req := aiFailureRequest(a, first.AttemptID, domain.AIFailureUnknown, reason)
			foreign := req
			foreign.OrganizationID = b
			_, err = r.FinishFailure(ctx, foreign)
			require.ErrorIs(t, err, domain.ErrAISettlementNotFound)
			out, err := r.FinishFailure(ctx, req)
			require.NoError(t, err)
			require.Equal(t, "unknown", out.AttemptStatus)
			require.Equal(t, "needs_reconciliation", out.OperationStatus)
			require.Nil(t, out.ChargedMicrocredits)
			require.Zero(t, out.ReleasedMicrocredits)
			var held int64
			require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&held))
			require.Equal(t, int64(10_000), held)
			_, err = r.FinishFailure(ctx, aiFailureRequest(a, first.AttemptID, domain.AIFailureNotDispatched, reason))
			require.ErrorIs(t, err, domain.ErrAISettlementConflict, "cancellation/timeout cannot prove non-dispatch")
			proof := aiFailureRequest(a, first.AttemptID, domain.AIFailureNotExecuted, reason)
			invalid := proof
			invalid.EvidenceReference = nil
			_, err = r.FinishFailure(ctx, invalid)
			require.ErrorIs(t, err, domain.ErrAISettlementInvalid)
			confirmed, err := r.FinishFailure(ctx, proof)
			require.NoError(t, err)
			require.Equal(t, "rejected", confirmed.AttemptStatus)
			require.Equal(t, int64(10_000), confirmed.ReleasedMicrocredits)
			if reason == "client_cancelled" {
				require.Equal(t, "cancelled", confirmed.OperationStatus)
			} else {
				require.Equal(t, "failed", confirmed.OperationStatus)
			}
			// Replaying an earlier unknown event cannot resurrect its old hold.
			replay, err := r.FinishFailure(ctx, req)
			require.NoError(t, err)
			require.False(t, replay.Applied)
			require.Equal(t, "rejected", replay.AttemptStatus)
		})
	}
}

func TestAIFailureConfirmedCost(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "paid-error", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, a, first.AttemptID)
	require.NoError(t, err)
	req := aiFailureRequest(a, first.AttemptID, domain.AIFailureCharged, "provider_error")
	usage := aiSettleRequest(a, first.AttemptID, 20, 0)
	req.Usage = &usage
	out, err := r.FinishFailure(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "settled", out.AttemptStatus)
	require.Equal(t, "failed", out.OperationStatus)
	require.Equal(t, int64(20_000), *out.ChargedMicrocredits, "charge even an error response, including overrun")
	replay, err := r.FinishFailure(ctx, req)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	late, err := r.SettleSuccess(ctx, usage)
	require.NoError(t, err)
	require.False(t, late.Applied)
	require.Equal(t, "failed", late.OperationStatus, "late usage does not turn a failed chain into success")
	var used int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used))
	require.Equal(t, int64(20_000), used)
}

func TestAIFailurePartialChain(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	req := aiReserveRequest(a, &uid, "partial-chain", 10_000)
	attempts := make([]domain.AIReservation, 0, 3)
	for i := int32(1); i <= 3; i++ {
		req.AttemptNumber = i
		out, err := r.Reserve(ctx, req)
		require.NoError(t, err)
		attempts = append(attempts, out)
	}
	for i := 0; i < 2; i++ {
		claimed, err := r.MarkSent(ctx, a, attempts[i].AttemptID)
		require.NoError(t, err)
		require.True(t, claimed)
	}
	_, err := r.SettleSuccess(ctx, aiSettleRequest(a, attempts[0].AttemptID, 2, 1))
	require.NoError(t, err)
	_, err = r.FinishFailure(ctx, aiFailureRequest(a, attempts[1].AttemptID, domain.AIFailureUnknown, "timeout"))
	require.NoError(t, err)
	last, err := r.FinishFailure(ctx, aiFailureRequest(a, attempts[2].AttemptID, domain.AIFailureNotDispatched, "local_error"))
	require.NoError(t, err)
	require.Equal(t, "needs_reconciliation", last.OperationStatus)
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, attempts[0].CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Equal(t, int64(10_000), held)
	resolved, err := r.SettleSuccess(ctx, aiSettleRequest(a, attempts[1].AttemptID, 3, 1))
	require.NoError(t, err)
	require.Equal(t, "failed", resolved.OperationStatus)
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, attempts[0].CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(11_000), used)
	require.Zero(t, held)
}

func TestAIFailureConcurrentCompletion(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "concurrent-failure", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, a, first.AttemptID)
	require.NoError(t, err)
	failure := aiFailureRequest(a, first.AttemptID, domain.AIFailureUnknown, "client_cancelled")
	start := make(chan struct{})
	results := make(chan error, 13)
	var wg sync.WaitGroup
	for i := 0; i < 13; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			call, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if i == 12 {
				_, err := r.SettleSuccess(call, aiSettleRequest(a, first.AttemptID, 2, 1))
				results <- err
			} else {
				_, err := r.FinishFailure(call, failure)
				results <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var used, held int64
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Zero(t, held)
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_operations WHERE id=$1`, first.OperationID).Scan(&status))
	require.Equal(t, "cancelled", status)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_failure_events`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAIFailureClaimRaceAndRollback(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "claim-race", 10_000))
	require.NoError(t, err)
	start := make(chan struct{})
	claim := make(chan bool, 1)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		v, e := r.MarkSent(ctx, a, first.AttemptID)
		claim <- v
		results <- e
	}()
	go func() {
		defer wg.Done()
		<-start
		_, e := r.FinishFailure(ctx, aiFailureRequest(a, first.AttemptID, domain.AIFailureNotDispatched, "local_error"))
		results <- e
	}()
	close(start)
	wg.Wait()
	close(results)
	claimed := <-claim
	errors := 0
	for err := range results {
		if err != nil {
			require.ErrorIs(t, err, domain.ErrAISettlementConflict)
			errors++
		}
	}
	if claimed {
		require.Equal(t, 1, errors)
	} else {
		require.Zero(t, errors)
	}
	second, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "failure-rollback", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE FUNCTION reject_failure_event_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure evidence insert'; END $$; CREATE TRIGGER reject_failure_event_test BEFORE INSERT ON ai_usage_failure_events FOR EACH ROW EXECUTE FUNCTION reject_failure_event_test()`)
	require.NoError(t, err)
	for _, outcome := range []domain.AIFailureOutcome{domain.AIFailureNotDispatched, domain.AIFailureCharged} {
		req := aiFailureRequest(a, second.AttemptID, outcome, "local_error")
		if outcome == domain.AIFailureCharged {
			usage := aiSettleRequest(a, second.AttemptID, 2, 1)
			req.Usage = &usage
		}
		_, err = r.FinishFailure(ctx, req)
		require.Error(t, err)
		var status, resStatus string
		require.NoError(t, db.QueryRow(ctx, `SELECT a.status,r.status FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1`, second.AttemptID).Scan(&status, &resStatus))
		require.Equal(t, "reserved", status)
		require.Equal(t, "active", resStatus)
	}
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_settlements WHERE attempt_id=$1`, second.AttemptID).Scan(&count))
	require.Zero(t, count)
}

func TestAIFailureLegacyMigration(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100600_ai_failure_outcomes.sql")
	require.NoError(t, err)
	parts := strings.Split(string(data), "-- +goose Down")
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err)
	uid := int64(3)
	legacy, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "legacy-unclaimed", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	_, err = r.FinishFailure(ctx, aiFailureRequest(a, legacy.AttemptID, domain.AIFailureNotDispatched, "local_error"))
	require.ErrorIs(t, err, domain.ErrAISettlementConflict)
	claimed, err := r.MarkSent(ctx, a, legacy.AttemptID)
	require.NoError(t, err)
	require.False(t, claimed)
	out, err := r.FinishFailure(ctx, aiFailureRequest(a, legacy.AttemptID, domain.AIFailureUnknown, "timeout"))
	require.NoError(t, err)
	require.Equal(t, "needs_reconciliation", out.OperationStatus)
	_, err = db.Exec(ctx, parts[1])
	require.Error(t, err, "do not delete failure evidence on rollback")
}

func TestAIFailureReleaseAfterTransfer(t *testing.T) {
	ctx, db, r, admin, a, b, now := newAISettlementTestDB(t)
	uid := int64(3)
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "late-proof", 10_000))
	require.NoError(t, err)
	claimed, err := r.MarkSent(ctx, a, first.AttemptID)
	require.NoError(t, err)
	require.True(t, claimed)
	*now = now.Add(168 * time.Hour)
	later, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "later-hold", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &b, aiBudgetRequest(100_000, 100_000, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 4, 3, nil, aiUserQuotaRequest(100_000, 100_000, 0, 1)))
	moved, err := r.Reserve(ctx, aiReserveRequest(b, &uid, "foreign-hold", 10_000))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now(),is_active=false WHERE id=3`)
	require.NoError(t, err)
	out, err := r.FinishFailure(ctx, aiFailureRequest(a, first.AttemptID, domain.AIFailureNotExecuted, "provider_error"))
	require.NoError(t, err)
	require.Equal(t, int64(10_000), out.ReleasedMicrocredits)
	var held, carried, used int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits,used_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, later.CompanyPeriodID).Scan(&held, &carried, &used))
	require.Equal(t, int64(10_000), held)
	require.Zero(t, carried)
	require.Zero(t, used)
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits,used_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, moved.FiveHourPeriodID).Scan(&held, &carried, &used))
	require.Equal(t, int64(20_000), held)
	require.Zero(t, carried)
	require.Zero(t, used)
}

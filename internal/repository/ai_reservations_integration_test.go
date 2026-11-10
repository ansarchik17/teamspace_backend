package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
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

func newAIReserveTestDB(t *testing.T) (context.Context, *pgxpool.Pool, *AIReservationRepository, *OrganizationRepository, uuid.UUID, uuid.UUID, *time.Time) {
	t.Helper()
	ctx, db, admin, a, b := newAIQuotaWriteDB(t)
	for _, file := range []string{"20261012000200_company_user_blocks.sql", "20261020100400_ai_employee_lifecycle.sql", "20261020100700_ai_attempt_request_identity.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	repo := NewAIReservationRepository(db, func() time.Time { return now })
	return ctx, db, repo, admin, a, b, &now
}

func aiReserveRequest(org uuid.UUID, user *int64, key string, amount int64) domain.AIReserveRequest {
	return domain.AIReserveRequest{OrganizationID: org, UserID: user, OperationKind: "chat", IdempotencyKey: key, Fingerprint: sha256.Sum256([]byte(key)), AttemptFingerprint: sha256.Sum256([]byte("provider-payload:" + key)), AttemptNumber: 1, Model: domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: "openai/gpt-oss-120b", PolicyVersion: domain.AICreditPolicyV1}, UsageAdapterVersion: "test-v1", Microcredits: amount}
}

func requireReserveDenied(t *testing.T, err error, reason string) *domain.AIReservationDenied {
	t.Helper()
	var denied *domain.AIReservationDenied
	require.ErrorAs(t, err, &denied)
	require.Equal(t, reason, denied.Reason)
	return denied
}

func TestAIReserveLimitsAndIdempotency(t *testing.T) {
	ctx, db, r, admin, a, b, now := newAIReserveTestDB(t)
	uid := int64(3)
	req := aiReserveRequest(a, &uid, "one", 60)
	var err error
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(300, 100, 100, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(200, 100, 0, 1)))
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, uid, func() time.Time { return *now }, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Equal(t, 100, *limits.FiveHour.RemainingPercent)
	require.Equal(t, 100, *limits.Weekly.RemainingPercent)
	require.True(t, limits.Access.CanStartRequest)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "too-big", 101))
	tooLarge := requireReserveDenied(t, err, "RESERVATION_TOO_LARGE")
	require.Nil(t, tooLarge.RecheckAt)
	require.EqualValues(t, 100, *tooLarge.AvailableMicrocredits)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_weekly_anchors`).Scan(&count))
	require.Zero(t, count, "rejected first request must not set an anchor")
	first, err := r.Reserve(ctx, req)
	require.NoError(t, err)
	require.True(t, first.Created)
	replay, err := r.Reserve(ctx, req)
	require.NoError(t, err)
	require.False(t, replay.Created)
	require.Equal(t, first.ReservationID, replay.ReservationID)
	changed := req
	changed.Microcredits++
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict)
	changed = req
	changed.Fingerprint = sha256.Sum256([]byte("different"))
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict)
	changed = req
	changed.IdempotencyKey = "unknown-model"
	changed.Model.ModelID = "unknown"
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAICreditUnknownModel)
	changed = req
	changed.Microcredits = 0
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAIReservationInvalid)
	_, err = r.Reserve(ctx, aiReserveRequest(b, &uid, "foreign", 1))
	requireReserveDenied(t, err, "AI_ACCESS_DENIED")
	require.NoError(t, admin.SetEmployeeBlocked(ctx, 2, 3, nil, true))
	_, err = r.Reserve(ctx, req)
	requireReserveDenied(t, err, "AI_ACCESS_DENIED")
	require.NoError(t, admin.SetEmployeeBlocked(ctx, 2, 3, nil, false))
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "second", 41))
	denied := requireReserveDenied(t, err, "RESERVATION_PENDING")
	require.Nil(t, denied.RecheckAt)
	// New actual attempt of the same operation must reserve again.
	changed = req
	changed.AttemptNumber = 2
	changed.Microcredits = 40
	second, err := r.Reserve(ctx, changed)
	require.NoError(t, err)
	require.Equal(t, first.OperationID, second.OperationID)
	require.NotEqual(t, first.ReservationID, second.ReservationID)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "third", 1))
	requireReserveDenied(t, err, "RESERVATION_PENDING")
	// A current-period usage counter must count independently of live holds.
	_, err = db.Exec(ctx, `UPDATE ai_user_weekly_periods SET used_microcredits=200 WHERE organization_id=$1`, a)
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "spent", 1))
	requireReserveDenied(t, err, "USER_WEEKLY_EXHAUSTED")
	service, err := r.Reserve(ctx, aiReserveRequest(a, nil, "index", 150))
	require.NoError(t, err)
	require.Nil(t, service.FiveHourPeriodID)
	_, err = r.Reserve(ctx, aiReserveRequest(a, nil, "index2", 51))
	requireReserveDenied(t, err, "RESERVATION_PENDING")
	var reserved int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, first.CompanyPeriodID).Scan(&reserved))
	require.Equal(t, int64(250), reserved)
}

func TestAIReserveConcurrent(t *testing.T) {
	for _, scope := range []string{"company", "weekly", "five_hour", "service", "idempotency"} {
		t.Run(scope, func(t *testing.T) {
			ctx, db, r, admin, a, _, _ := newAIReserveTestDB(t)
			require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 1000, 0, 0)))
			require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(1000, 1000, 0, 1)))
			// Fixtures deliberately allow over-assignment to isolate the company
			// gate from personal gates; production administration forbids it.
			if scope == "company" {
				_, err := db.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,2,1000,1000)`, a)
				require.NoError(t, err)
			}
			if scope == "weekly" {
				require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 100, 1, 1)))
			}
			if scope == "five_hour" {
				require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(1000, 100, 1, 1)))
			}
			if scope == "service" {
				require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(0, 0, 1, 1)))
				require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(100, 1000, 100, 1)))
			}
			start := make(chan struct{})
			type result struct {
				reservation domain.AIReservation
				err         error
			}
			results := make(chan result, 24)
			var wg sync.WaitGroup
			for i := 0; i < 24; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					call, cancel := context.WithTimeout(ctx, 15*time.Second)
					defer cancel()
					user := int64(3)
					key := fmt.Sprintf("parallel-%d", i)
					amount := int64(10)
					if scope == "company" {
						user = int64(2 + i%2)
						amount = 100
					}
					if scope == "idempotency" {
						key = "same"
					}
					req := aiReserveRequest(a, &user, key, amount)
					if scope == "service" {
						req.UserID = nil
					}
					out, err := r.Reserve(call, req)
					results <- result{out, err}
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			created := 0
			for result := range results {
				if result.err == nil {
					if result.reservation.Created {
						created++
					}
					continue
				}

				requireReserveDenied(t, result.err, "RESERVATION_PENDING")
			}
			want, amount := 10, int64(100)
			if scope == "company" {
				want = 20
				amount = 2000
			}
			if scope == "idempotency" {
				want = 1
				amount = 10
			}
			require.Equal(t, want, created)
			var held int64
			require.NoError(t, db.QueryRow(ctx, `SELECT COALESCE(SUM(reserved_microcredits),0)::bigint FROM ai_usage_reservations WHERE status='active'`).Scan(&held))
			require.Equal(t, amount, held)
			var mirror int64
			require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE organization_id=$1`, a).Scan(&mirror))
			require.Equal(t, held, mirror)
		})
	}
}

func TestAIReservePeriodRolloverAndTransfer(t *testing.T) {
	ctx, db, r, admin, a, b, now := newAIReserveTestDB(t)
	uid := int64(3)
	for _, org := range []uuid.UUID{a, b} {
		require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(1000, 100, 0, 0)))
	}
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(500, 100, 0, 1)))
	first, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "old", 80))
	require.NoError(t, err)
	*now = now.Add(7 * time.Hour)
	second, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "new", 20))
	require.NoError(t, err)
	require.NotEqual(t, first.FiveHourPeriodID, second.FiveHourPeriodID)
	var held, carried int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, second.FiveHourPeriodID).Scan(&held, &carried))
	require.Equal(t, int64(20), held)
	require.Zero(t, carried)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 4, 3, nil, aiUserQuotaRequest(500, 100, 0, 1)))
	_, err = r.Reserve(ctx, aiReserveRequest(b, &uid, "too-large-in-live-window", 81))
	requireReserveDenied(t, err, "RESERVATION_PENDING")
	_, err = r.Reserve(ctx, aiReserveRequest(b, &uid, "old", 80))
	require.ErrorIs(t, err, domain.ErrAIReservationConflict, "key cannot create a new operation after transfer")
	// Increasing Hmax/Q/H does not erase old-company global personal holds.
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &b, aiBudgetRequest(1000, 200, 0, 1)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 4, 3, nil, aiUserQuotaRequest(500, 200, 1, 2)))
	third, err := r.Reserve(ctx, aiReserveRequest(b, &uid, "moved", 50))
	require.NoError(t, err)
	require.Equal(t, second.FiveHourPeriodID, third.FiveHourPeriodID)
	var companyHeld int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, third.CompanyPeriodID).Scan(&companyHeld))
	require.Equal(t, int64(50), companyHeld, "old holds must not consume the new company budget")
	// Return to A more than a week later: all unresolved weekly holds carry.
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, a)
	require.NoError(t, err)
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 100, 2, 1)))
	*now = now.Add(168 * time.Hour)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "still-held-weekly", 1))
	requireReserveDenied(t, err, "RESERVATION_PENDING")
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 200, 0, 1)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(500, 200, 3, 2)))
	last, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "after-week", 10))
	require.NoError(t, err)
	require.NotEqual(t, first.CompanyPeriodID, last.CompanyPeriodID)
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits FROM organization_ai_weekly_periods WHERE id=$1`, last.CompanyPeriodID).Scan(&held, &carried))
	require.Equal(t, int64(110), held)
	require.Equal(t, int64(100), carried)
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits,carried_reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, last.FiveHourPeriodID).Scan(&held, &carried))
	require.Equal(t, int64(10), held)
	require.Zero(t, carried)
}

func TestAIReserveRollbackAndDisabled(t *testing.T) {
	ctx, db, r, admin, a, _, _ := newAIReserveTestDB(t)
	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 0, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(100, 100, 0, 1)))
	_, err := db.Exec(ctx, `CREATE FUNCTION reject_reservation_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test reservation failure'; END $$;
 CREATE TRIGGER reject_reservation_test BEFORE INSERT ON ai_usage_reservations FOR EACH ROW EXECUTE FUNCTION reject_reservation_test()`)
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "rollback", 10))
	require.Error(t, err)
	for _, table := range []string{"organization_ai_weekly_anchors", "user_ai_five_hour_anchors", "organization_ai_weekly_periods", "user_ai_five_hour_periods", "organization_ai_user_weekly_usage", "ai_usage_operations", "ai_usage_attempts", "ai_usage_reservations"} {
		var count int
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, table)
	}
	_, err = db.Exec(ctx, `DROP TRIGGER reject_reservation_test ON ai_usage_reservations`)
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(a, nil, "no-service", 1))
	require.NoError(t, err, "background requests require no separate service allocation")
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(0, 100, 0, 1)))
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "off", 1))
	require.NoError(t, err, "company budget does not disable personal access")
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 0, 0, 2)))
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "hmax-off", 1))
	require.NoError(t, err, "company Hmax does not disable personal access")
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000, 100, 0, 3)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(0, 0, 1, 4)))
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "user-off", 1))
	requireReserveDenied(t, err, "USER_QUOTA_DISABLED")
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(a, &uid, "suspended", 1))
	requireReserveDenied(t, err, "COMPANY_SUSPENDED")
}

func TestAIReserveDurableConcurrencyAcrossInstances(t *testing.T) {
	ctx, db, _, admin, org, _, now := newAIReserveTestDB(t)
	installAIConcurrencyReconciliationFixture(t, ctx, db)
	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(300, 100, 100, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(200, 100, 0, 1)))
	firstRepo := NewAIReservationRepository(db, func() time.Time { return *now }, WithAIUserConcurrencyLimit(1))
	secondRepo := NewAIReservationRepository(db, func() time.Time { return *now }, WithAIUserConcurrencyLimit(1))
	req := aiReserveRequest(org, &uid, "durable-first", 10)
	first, err := firstRepo.Reserve(ctx, req)
	require.NoError(t, err)
	replay, err := secondRepo.Reserve(ctx, req)
	require.NoError(t, err)
	require.Equal(t, first.ReservationID, replay.ReservationID)
	*now = now.Add(8 * 24 * time.Hour)
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, uid, func() time.Time { return *now }, "enforced")
	require.NoError(t, err)
	require.Zero(t, limits.FiveHour.CarriedReserved)
	require.Equal(t, int64(10), limits.Weekly.CarriedReserved)
	require.Nil(t, limits.FiveHour.PeriodID)
	require.Nil(t, limits.Weekly.PeriodID)

	_, err = secondRepo.Reserve(ctx, aiReserveRequest(org, &uid, "durable-second", 10))
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded, "old unresolved holds survive Redis TTL and accounting period boundaries")
}

func TestAIReserveDurableConcurrencyParallel(t *testing.T) {
	ctx, db, _, admin, org, _, now := newAIReserveTestDB(t)
	installAIConcurrencyReconciliationFixture(t, ctx, db)
	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(300, 100, 100, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(200, 100, 0, 1)))
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			repo := NewAIReservationRepository(db, func() time.Time { return *now }, WithAIUserConcurrencyLimit(1))
			_, err := repo.Reserve(ctx, aiReserveRequest(org, &uid, fmt.Sprintf("parallel-slot-%d", i), 1))
			results <- err
		}(i)
	}
	success := 0
	for i := 0; i < 8; i++ {
		err := <-results
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)
		}
	}
	require.Equal(t, 1, success)
}

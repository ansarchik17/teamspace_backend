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
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func finalQuotaFixture(t *testing.T) (context.Context, *pgxpool.Pool, *AIRepository, domain.AIPaidCaller, string, string) {
	t.Helper()
	ctx, db, _, admin, org, _, _ := newAISettlementTestDB(t)
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, 3, nil, aiUserQuotaRequest(1000000, 100000, 1, 1)))
	ddl, err := os.ReadFile("../../db/migrations/026_create_ai_tables.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(ddl), "-- +goose Down")[0])
	require.NoError(t, err)
	var chat, turn string
	require.NoError(t, db.QueryRow(ctx, `INSERT INTO ai_conversations(user_id,model_name) VALUES(3,'openai/gpt-oss-120b') RETURNING id`).Scan(&chat))
	require.NoError(t, db.QueryRow(ctx, `INSERT INTO ai_messages(conversation_id,role,content) VALUES($1,'user','last prompt') RETURNING id`, chat).Scan(&turn))
	caller, err := domain.NewAIPaidUserCaller(org, 3)
	require.NoError(t, err)
	return ctx, db, NewAIRepository(db, zap.NewNop()), caller, chat, turn
}

func TestFinalQuotaCutoffSerializesCompetingTurnsAndReplays(t *testing.T) {
	ctx, db, r, caller, chat, turn := finalQuotaFixture(t)
	var other string
	require.NoError(t, db.QueryRow(ctx, `INSERT INTO ai_messages(conversation_id,role,content) VALUES($1,'user','concurrent prompt') RETURNING id`, chat).Scan(&other))
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for _, source := range []string{turn, other} {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			result, err := r.CompleteFinalQuotaTurn(ctx, caller, chat, source, "five_hour", "Лимит исчерпан", 100000, false, 0)
			require.NoError(t, err)
			if result != nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(source)
	}
	wg.Wait()
	require.Equal(t, 1, success)
	var count int
	var amount int64
	var winner, assistant string
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_final_request_cutoffs`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(ctx, `SELECT source_message_id,assistant_message_id,microcredits FROM ai_final_request_cutoffs`).Scan(&winner, &assistant, &amount))
	require.EqualValues(t, 100000, amount)
	replay, err := r.CompleteFinalQuotaTurn(ctx, caller, chat, winner, "five_hour", "must not replace persisted notice", 100000, false, 0)
	require.NoError(t, err)
	require.Equal(t, assistant, replay.MessageID)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_messages WHERE role='assistant'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_attempts`).Scan(&count))
	require.Zero(t, count, "no invented provider expense or token counters")
	limits, err := NewOrganizationRepository(db, zap.NewNop()).GetUserAILimits(ctx, *caller.UserID)
	require.NoError(t, err)
	require.Equal(t, "exhausted", limits.FiveHour.State)
	require.Zero(t, *limits.FiveHour.Available)
	require.EqualValues(t, 900000, *limits.Weekly.Available)
	_, err = NewAIReservationRepository(db, nil).Reserve(ctx, aiReserveRequest(caller.OrganizationID, caller.UserID, "after-cutoff", 1))
	requireReserveDenied(t, err, "FIVE_HOUR_EXHAUSTED")
	_, err = db.Exec(ctx, `UPDATE ai_final_request_cutoffs SET microcredits=0`)
	require.Error(t, err)
	_, err = db.Exec(ctx, `DELETE FROM ai_conversations WHERE id=$1`, chat)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_final_request_cutoffs`).Scan(&count))
	require.Equal(t, 1, count, "history deletion cannot refund the cutoff")
}

func TestFinalQuotaCutoffRollbackAndStaleAdmission(t *testing.T) {
	for _, scenario := range []string{"rollback", "stale", "foreign", "held"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, r, caller, chat, turn := finalQuotaFixture(t)
			expected := int64(100000)
			if scenario == "rollback" {
				_, err := db.Exec(ctx, `CREATE FUNCTION reject_cutoff_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure';END $$;CREATE TRIGGER reject_cutoff_update BEFORE UPDATE ON user_ai_five_hour_periods FOR EACH ROW EXECUTE FUNCTION reject_cutoff_update()`)
				require.NoError(t, err)
			}
			if scenario == "stale" {
				expected = 5000
			}
			if scenario == "foreign" {
				caller, _ = domain.NewAIPaidUserCaller(caller.OrganizationID, 2)
			}
			if scenario == "held" {
				_, err := NewAIReservationRepository(db, nil).Reserve(ctx, aiReserveRequest(caller.OrganizationID, caller.UserID, "active-work", 20000))
				require.NoError(t, err)
				expected = 80000
			}
			result, err := r.CompleteFinalQuotaTurn(ctx, caller, chat, turn, "five_hour", "limit notice", expected, false, 0)
			if scenario == "rollback" || scenario == "foreign" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Nil(t, result)
			var count int
			require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_final_request_cutoffs`).Scan(&count))
			require.Zero(t, count)
			require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_messages WHERE role='assistant'`).Scan(&count))
			require.Zero(t, count)
			if scenario != "held" {
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_user_weekly_periods`).Scan(&count))
				require.Zero(t, count, "failed or stale cutoff must not open periods")
			}
		})
	}
}

func TestFinalQuotaCutoffKeepsOldHoldsWithoutBlockingFreshFiveHours(t *testing.T) {
	for _, scenario := range []string{"old_hold", "weekly_insufficient", "no_company"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, r, caller, chat, turn := finalQuotaFixture(t)
			hold := int64(20000)
			if scenario == "weekly_insufficient" {
				hold = 95000
				_, err := db.Exec(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=100000 WHERE user_id=$1`, *caller.UserID)
				require.NoError(t, err)
			}
			if scenario == "no_company" {
				_, err := db.Exec(ctx, `UPDATE users SET organization_id=NULL WHERE id=$1`, *caller.UserID)
				require.NoError(t, err)
				_, err = db.Exec(ctx, `INSERT INTO user_ai_personal_quotas(user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES(3,1000000,100000)`)
				require.NoError(t, err)
				caller, err = domain.NewAIPaidUserCaller(uuid.Nil, *caller.UserID)
				require.NoError(t, err)
			}
			old := NewAIReservationRepository(db, func() time.Time { return time.Now().Add(-6 * time.Hour) })
			reservation, err := old.Reserve(ctx, aiReserveRequest(caller.OrganizationID, caller.UserID, "old-hold", hold))
			require.NoError(t, err)
			result, err := r.CompleteFinalQuotaTurn(ctx, caller, chat, turn, "five_hour", "limit notice", 100000, false, 0)
			require.NoError(t, err)
			if scenario == "weekly_insufficient" {
				require.Nil(t, result, "weekly free balance cannot fund exhaustion of five hours")
			} else {
				require.NotNil(t, result)
				require.EqualValues(t, 100000, result.Microcredits)
				limits, err := NewOrganizationRepository(db, zap.NewNop()).GetUserAILimits(ctx, *caller.UserID)
				require.NoError(t, err)
				require.EqualValues(t, 100000, limits.FiveHour.Used)
				require.Zero(t, limits.FiveHour.Reserved)
				require.EqualValues(t, hold, limits.Weekly.Reserved)
				require.EqualValues(t, 880000, *limits.Weekly.Available)
			}
			var held int64
			require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM ai_usage_reservations WHERE id=$1 AND status='active'`, reservation.ReservationID).Scan(&held))
			require.Equal(t, hold, held, "cutoff must never release or spend an earlier reservation")
		})
	}
}

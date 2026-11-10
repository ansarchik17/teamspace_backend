package repository

import (
	"context"
	"crypto/sha256"
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

func newAIEnforcementTestDB(t *testing.T) (context.Context, *pgxpool.Pool, *OrganizationRepository, uuid.UUID, uuid.UUID, *time.Time) {
	t.Helper()
	ctx, db, admin, a, b := newAIQuotaWriteDB(t)
	migrations := []string{
		"20261012000200_company_user_blocks.sql",
		"20261020100400_ai_employee_lifecycle.sql",
		"20261020100500_ai_usage_settlements.sql",
		"20261020100600_ai_failure_outcomes.sql",
		"20261020100700_ai_attempt_request_identity.sql",
		"20261020100800_ai_reservation_reconciliation.sql",
		"20261020101000_ai_budget_observations.sql",
	}
	for _, file := range migrations {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	return ctx, db, admin, a, b, &now
}

func testReserveReq(org uuid.UUID, user *int64, key string, amount int64) domain.AIReserveRequest {
	return domain.AIReserveRequest{
		OrganizationID:      org,
		UserID:              user,
		OperationKind:       "chat",
		IdempotencyKey:      key,
		Fingerprint:         sha256.Sum256([]byte(key)),
		AttemptFingerprint:  sha256.Sum256([]byte("payload:" + key)),
		AttemptNumber:       1,
		Model:               domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: "openai/gpt-oss-120b", PolicyVersion: domain.AICreditPolicyV1},
		UsageAdapterVersion: "test-v1",
		Microcredits:        amount * 1000,
	}
}

func testSettleReq(org, attempt uuid.UUID, chargedTokens int64) domain.AISettleRequest {
	return domain.AISettleRequest{
		OrganizationID:      org,
		AttemptID:           attempt,
		ActualModel:         "openai/gpt-oss-120b",
		UsageAdapterVersion: "test-v1",
		UsageSource:         "provider_api",
		Usage: domain.AICreditUsage{
			Status:       "verified",
			InputTokens:  &chargedTokens,
			OutputTokens: new(int64),
			TotalTokens:  &chargedTokens,
		},
	}
}

// 1. Превышение лимита в режимах enforced и observe
func TestAIEnforcementMode_QuotaExceeded(t *testing.T) {
	ctx, db, admin, a, _, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }

	// Настраиваем бюджет 1000 и квоту пользователя 100 (5-hour limit 100)
	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000000, 100000, 100000, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(100000, 100000, 0, 1)))

	enforcedRepo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeEnforced))
	observeRepo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))

	// Запрос 1: на 80 микрокредитов через enforcedRepo — успешен
	req1 := testReserveReq(a, &uid, "op-1", 80)
	res1, err := enforcedRepo.Reserve(ctx, req1)
	require.NoError(t, err)
	require.True(t, res1.Created)

	// Settle 80 микрокредитов (input=80)
	_, err = enforcedRepo.SettleSuccess(ctx, testSettleReq(a, res1.AttemptID, 80))
	require.NoError(t, err)

	// Запрос 2: на 50 микрокредитов (остаток 20, нужно 50)
	req2 := testReserveReq(a, &uid, "op-2", 50)

	// Остаток 20 положителен, но не покрывает запрос 50: это не исчерпание.
	_, err = enforcedRepo.Reserve(ctx, req2)
	require.Error(t, err)
	var denied *domain.AIReservationDenied
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "RESERVATION_TOO_LARGE", denied.Reason)

	// В режиме observe: тот же самый запрос НЕ должен быть отклонен!
	res2, err := observeRepo.Reserve(ctx, req2)
	require.NoError(t, err, "observe mode must not reject request due to exhausted quota")
	require.True(t, res2.Created)

	// Успешный settle второго запроса: перерасход фиксируется
	settle2, err := observeRepo.SettleSuccess(ctx, testSettleReq(a, res2.AttemptID, 50))
	require.NoError(t, err)
	require.True(t, settle2.Applied)

	// Проверяем, что в периоде used_microcredits = 80 + 50 = 130 (превысило лимит 100!)
	var fiveUsed int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, res2.FiveHourPeriodID).Scan(&fiveUsed))
	require.Equal(t, int64(130000), fiveUsed)
	var exceeded bool
	require.NoError(t, db.QueryRow(ctx, `SELECT exceeded_at_admission FROM ai_usage_budget_exceedances WHERE attempt_id=$1 AND scope='weekly'`, res2.AttemptID).Scan(&exceeded))
	require.True(t, exceeded)

}

// 2. Отсутствующая настройка (Unconfigured budget / quota)
func TestAIEnforcementMode_UnconfiguredBudget(t *testing.T) {
	ctx, db, admin, a, _, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }

	// Никакого бюджета и квот не настраиваем (COMPANY_BUDGET_UNCONFIGURED)
	uid := int64(3)
	req := testReserveReq(a, &uid, "unconf-1", 50)

	enforcedRepo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeEnforced))
	observeRepo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))

	// Both modes provide default personal limits without company configuration.
	_, err := enforcedRepo.Reserve(ctx, testReserveReq(a, &uid, "enforced-default", 50))
	require.NoError(t, err)
	// В режиме observe: создается и учитывается
	res, err := observeRepo.Reserve(ctx, req)
	require.NoError(t, err, "observe mode must allow unconfigured company requests")
	require.True(t, res.Created)

	// Settle записывает расход в периоды компании
	settle, err := observeRepo.SettleSuccess(ctx, testSettleReq(a, res.AttemptID, 50))
	require.NoError(t, err)
	require.True(t, settle.Applied)

	// Проверяем запись в журнале операций
	var opCount int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_operations WHERE id=$1`, res.OperationID).Scan(&opCount))
	require.Equal(t, 1, opCount)
	admin.SetEnforcementMode(domain.AIEnforcementModeObserve)
	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, uid, clock, domain.AIEnforcementModeObserve)
	require.NoError(t, err)
	require.Equal(t, int64(50000), limits.Weekly.Used)
	require.True(t, limits.Access.CanStartRequest)
	require.Equal(t, domain.DefaultAIWeeklyMicrocredits, *limits.Weekly.Limit)
	var observations int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_budget_exceedances WHERE attempt_id=$1 AND limit_microcredits IS NULL`, res.AttemptID).Scan(&observations))
	require.Equal(t, 1, observations)

}

// 3. Переключение режима (observe -> enforced -> observe)
func TestAIEnforcementMode_Switching(t *testing.T) {
	ctx, db, admin, a, _, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }

	// Настраиваем лимит 50
	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000000, 50000, 50000, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(50000, 50000, 0, 1)))

	repo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))

	// 1. Вызов в observe на 60 (превышение)
	req1 := testReserveReq(a, &uid, "switch-1", 60)
	res1, err := repo.Reserve(ctx, req1)
	require.NoError(t, err)
	_, err = repo.SettleSuccess(ctx, testSettleReq(a, res1.AttemptID, 60))
	require.NoError(t, err)

	// 2. Переключаем режим на enforced
	repo.SetEnforcementMode(domain.AIEnforcementModeEnforced)

	// Проверяем, что журнал и периоды НЕ обнулились
	var opCount int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_operations WHERE id=$1`, res1.OperationID).Scan(&opCount))
	require.Equal(t, 1, opCount)

	// Следующий запрос в enforced должен быть отклонен из-за исчерпания
	req2 := testReserveReq(a, &uid, "switch-2", 10)
	_, err = repo.Reserve(ctx, req2)
	require.Error(t, err)
	var denied *domain.AIReservationDenied
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "USER_WEEKLY_EXHAUSTED", denied.Reason)

	// 3. Переключаем обратно на observe
	repo.SetEnforcementMode(domain.AIEnforcementModeObserve)

	// В observe снова проходит!
	res2, err := repo.Reserve(ctx, req2)
	require.NoError(t, err)
	require.True(t, res2.Created)
}

// 4. Нетарифицированная модель НЕ становится бесплатной в observe
func TestAIEnforcementMode_UnpricedModelNeverFree(t *testing.T) {
	ctx, db, admin, a, _, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }

	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000000, 500000, 500000, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(500000, 500000, 0, 1)))

	observeRepo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))

	req := testReserveReq(a, &uid, "unpriced-1", 50)
	req.Model.ModelID = "unpriced/unknown-model"

	_, err := observeRepo.Reserve(ctx, req)
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrAICreditUnknownModel, "unpriced model must be rejected even in observe mode")
}

// 5. Безопасность и нефинансовые проверки в observe mode
func TestAIEnforcementMode_SecurityBlockersPreserved(t *testing.T) {
	ctx, db, admin, a, _, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }

	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &a, aiBudgetRequest(1000000, 500000, 500000, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(500000, 500000, 0, 1)))

	observeRepo := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))

	// Приостановка компании:
	_, err := db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)

	req := testReserveReq(a, &uid, "sec-1", 50)
	_, err = observeRepo.Reserve(ctx, req)
	require.Error(t, err)
	var denied *domain.AIReservationDenied
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "COMPANY_SUSPENDED", denied.Reason)

	// Восстанавливаем компанию
	_, err = db.Exec(ctx, `UPDATE organizations SET status='active' WHERE id=$1`, a)
	require.NoError(t, err)

	// Блокировка пользователя (deleted_at)
	_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now() WHERE id=$1`, uid)
	require.NoError(t, err)

	_, err = observeRepo.Reserve(ctx, req)
	require.Error(t, err)
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "AI_ACCESS_DENIED", denied.Reason)
}

// 6. Concurrency / Multi-instance в observe mode
func TestAIEnforcementMode_MultiInstanceConcurrency(t *testing.T) {
	ctx, db, _, a, _, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }

	// Создаем несколько инстансов репозитория, имитирующих разные поды
	instance1 := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))
	instance2 := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve))

	uid := int64(3)
	otherUser := int64(2)
	var wg sync.WaitGroup
	errs := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, rErr := instance1.Reserve(ctx, testReserveReq(a, &uid, "par-1", 50))
		if rErr != nil {
			errs <- rErr
		}
	}()
	go func() {
		defer wg.Done()
		_, rErr := instance2.Reserve(ctx, testReserveReq(a, &otherUser, "par-2", 50))
		if rErr != nil {
			errs <- rErr
		}
	}()
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	// Оба параллельных запроса должны быть успешно сохранены в журнале
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_operations WHERE organization_id=$1`, a).Scan(&count))
	require.Equal(t, 2, count)
}

func TestAIEnforcementMode_ReplayAndUnknownHolds(t *testing.T) {
	ctx, db, _, org, _, now := newAIEnforcementTestDB(t)
	uid := int64(3)
	clock := func() time.Time { return *now }
	observe := NewAIReservationRepository(db, clock, WithAIEnforcementMode(domain.AIEnforcementModeObserve), WithAIUserConcurrencyLimit(1))
	req := testReserveReq(org, &uid, "observe-replay", 10)
	first, err := observe.Reserve(ctx, req)
	require.NoError(t, err)
	claimed, err := observe.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = observe.FinishFailure(ctx, domain.AIFailureRequest{OrganizationID: org, AttemptID: first.AttemptID, Outcome: domain.AIFailureUnknown, EventID: uuid.New(), Reason: "invalid_usage", EvidenceSource: "provider_response"})
	require.NoError(t, err)
	replay, err := observe.Reserve(ctx, req)
	require.NoError(t, err)
	require.False(t, replay.Created)
	require.Equal(t, first.AttemptID, replay.AttemptID)
	observe.SetAccountingEnabled(false)
	_, err = observe.Reserve(ctx, testReserveReq(org, &uid, "disabled", 1))
	require.ErrorIs(t, err, domain.ErrAIAccountingDisabled)
	observe.SetAccountingEnabled(true)
	*now = now.Add(8 * 24 * time.Hour)
	_, err = observe.Reserve(ctx, testReserveReq(org, &uid, "after-unknown", 1))
	denied := requireReserveDenied(t, err, "RESERVATION_PENDING")
	require.Equal(t, "account", denied.Scope)
	require.Nil(t, denied.RecheckAt, "unknown work cannot promise a reset time")
	var active, observations int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_reservations WHERE status='active'`).Scan(&active))
	require.Equal(t, 1, active)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_budget_observations`).Scan(&observations))
	require.Equal(t, 3, observations)
}

func TestAIEnforcementMode_ActualOverrunSnapshot(t *testing.T) {
	ctx, db, admin, org, _, now := newAIEnforcementTestDB(t)
	uid := int64(3)
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, aiBudgetRequest(1000000, 10000, 10000, 0)))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 2, uid, nil, aiUserQuotaRequest(10000, 10000, 0, 1)))
	repo := NewAIReservationRepository(db, func() time.Time { return *now }, WithAIEnforcementMode(domain.AIEnforcementModeObserve))
	reserved, err := repo.Reserve(ctx, testReserveReq(org, &uid, "actual-overrun", 1))
	require.NoError(t, err)
	_, err = repo.SettleSuccess(ctx, testSettleReq(org, reserved.AttemptID, 50))
	require.NoError(t, err)
	var admission, actual bool
	require.NoError(t, db.QueryRow(ctx, `SELECT exceeded_at_admission,exceeded_with_actual_usage FROM ai_usage_budget_exceedances WHERE attempt_id=$1 AND scope='weekly'`, reserved.AttemptID).Scan(&admission, &actual))
	require.False(t, admission)
	require.True(t, actual)
}

func TestAIEnforcementMode_StorageBounds(t *testing.T) {
	ctx, db, _, org, _, now := newAIEnforcementTestDB(t)
	uid := int64(3)
	repo := NewAIReservationRepository(db, func() time.Time { return *now }, WithAIEnforcementMode(domain.AIEnforcementModeObserve))
	reserved, err := repo.Reserve(ctx, testReserveReq(org, &uid, "storage-first", 1))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_ai_weekly_periods SET used_microcredits=9223372036854774807 WHERE id=$1`, reserved.CompanyPeriodID)
	require.NoError(t, err)
	_, err = repo.Reserve(ctx, testReserveReq(org, &uid, "storage-overflow", 1))
	require.ErrorIs(t, err, domain.ErrAICreditOverflow)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_attempts`).Scan(&count))
	require.Equal(t, 1, count, "storage bounds must rollback admission without a provider call")
}

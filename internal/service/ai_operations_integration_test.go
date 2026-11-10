package service

import (
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func newAIOperationServiceDB(t *testing.T) (context.Context, *pgxpool.Pool, *AIAccountingService, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "ai_operations_service_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, e)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,status TEXT NOT NULL DEFAULT 'active'); CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ,is_active BOOLEAN NOT NULL DEFAULT true);`)
	require.NoError(t, err)
	for _, file := range []string{"20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql", "20261022100200_ai_personal_weekly_windows.sql", "20261020100900_ai_enforcement_mode_unconfigured.sql", "20261022100400_ai_personal_access_policy.sql", "20261022100500_ai_final_request_cutoffs.sql", "20261020100400_ai_employee_lifecycle.sql", "20261020100500_ai_usage_settlements.sql", "20261020100600_ai_failure_outcomes.sql", "20261020100700_ai_attempt_request_identity.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, e)
	}
	org := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id) VALUES($1);`, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id) VALUES(1,$1)`, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,100000,100000)`, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES($1,1,100000,100000)`, org)
	require.NoError(t, err)
	return ctx, db, NewAIAccountingService(repository.NewAIReservationRepository(db, nil)), org
}

func serviceAIOperationRequest(org uuid.UUID, key string) domain.AIReserveRequest {
	user := int64(1)
	return domain.AIReserveRequest{OrganizationID: org, UserID: &user, IdempotencyKey: key, Fingerprint: sha256.Sum256([]byte("user-intent:" + key)), AttemptFingerprint: sha256.Sum256([]byte("provider-payload:" + key)), OperationKind: "chat", AttemptNumber: 1, Model: domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: "openai/gpt-oss-120b", PolicyVersion: domain.AICreditPolicyV1}, UsageAdapterVersion: "test-v1", Microcredits: 10_000}
}

func serviceAIUsage(model string) domain.AISettleRequest {
	i, o := int64(2), int64(1)
	return domain.AISettleRequest{ActualModel: model, UsageAdapterVersion: "test-v1", UsageSource: "provider_usage", Usage: domain.AICreditUsage{Status: "verified", InputTokens: &i, OutputTokens: &o}}
}

func TestAIOperationConcurrentExecutors(t *testing.T) {
	ctx, db, s, org := newAIOperationServiceDB(t)
	req := serviceAIOperationRequest(org, "same-user-operation")
	var calls atomic.Int32
	start := make(chan struct{})
	type result struct {
		reservation domain.AIReservation
		err         error
	}
	results := make(chan result, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out, _, err := s.RunSuccess(ctx, req, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
				calls.Add(1)
				return serviceAIUsage(req.Model.ModelID), nil
			})
			results <- result{out, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var operation, attempt, reservation uuid.UUID
	created := 0
	for out := range results {
		require.NoError(t, out.err)
		if out.reservation.Created {
			created++
		}
		if operation == uuid.Nil {
			operation, attempt, reservation = out.reservation.OperationID, out.reservation.AttemptID, out.reservation.ReservationID
		}
		require.Equal(t, operation, out.reservation.OperationID)
		require.Equal(t, attempt, out.reservation.AttemptID)
		require.Equal(t, reservation, out.reservation.ReservationID)
	}
	require.Equal(t, 1, created)
	require.Equal(t, int32(1), calls.Load(), "one logical request invokes provider only once")
	// A later retry must return stored usage even after routing/estimate changes.
	changed := req
	changed.Microcredits = 99_000
	changed.Model.ModelID = "new-unregistered-default"
	changed.AttemptFingerprint = sha256.Sum256([]byte("new-server-context"))
	out, settled, err := s.RunSuccess(ctx, changed, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
		calls.Add(1)
		return serviceAIUsage(req.Model.ModelID), nil
	})
	require.NoError(t, err)
	require.False(t, out.Created)
	require.Equal(t, domain.AIOperationCompleted, out.Disposition)
	require.NotNil(t, settled)
	require.False(t, settled.Applied)
	require.Equal(t, int64(5000), settled.ChargedMicrocredits)
	require.Equal(t, int32(1), calls.Load())
	var used, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&used, &held))
	require.Equal(t, int64(5000), used)
	require.Zero(t, held)
}

func TestAIOperationSeparatePaidAttempts(t *testing.T) {
	ctx, db, s, org := newAIOperationServiceDB(t)
	first := serviceAIOperationRequest(org, "server-chain")
	second := first
	second.AttemptNumber = 2
	second.Model.ModelID = "zai-org/GLM-4.6"
	second.AttemptFingerprint = sha256.Sum256([]byte("actual-fallback-request"))
	calls := 0
	var next domain.AIReservation
	var nestedErr error
	out, settled, err := s.RunSuccess(ctx, first, func(ctx context.Context, _ domain.AIReservation) (domain.AISettleRequest, error) {
		calls++
		next, _, nestedErr = s.RunSuccess(ctx, second, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
			calls++
			return serviceAIUsage(second.Model.ModelID), nil
		})
		return serviceAIUsage(first.Model.ModelID), nestedErr
	})
	require.NoError(t, err)
	require.NoError(t, nestedErr)
	require.Equal(t, 2, calls)
	require.NotNil(t, settled)
	require.Equal(t, out.OperationID, next.OperationID)
	require.NotEqual(t, out.AttemptID, next.AttemptID)
	require.NotEqual(t, out.ReservationID, next.ReservationID)
	var count int
	var charged int64
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*),SUM(charged_microcredits)::bigint FROM ai_usage_attempts WHERE operation_id=$1`, out.OperationID).Scan(&count, &charged))
	require.Equal(t, 2, count)
	require.Equal(t, int64(15_000), charged)
	replay, _, err := s.RunSuccess(ctx, first, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
		calls++
		return serviceAIUsage(first.Model.ModelID), nil
	})
	require.NoError(t, err)
	require.False(t, replay.Created)
	require.Equal(t, int32(2), replay.AttemptNumber, "logical retry returns latest chain state")
	require.Equal(t, domain.AIOperationCompleted, replay.Disposition)
	require.Equal(t, 2, calls)
	_, _, err = s.RunSuccess(ctx, second, func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
		calls++
		return serviceAIUsage(second.Model.ModelID), nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls, "the same actual attempt is not dispatched twice")
}

func TestAIOperationReplayStates(t *testing.T) {
	ctx, _, s, org := newAIOperationServiceDB(t)
	req := serviceAIOperationRequest(org, "recovery-state")
	first, err := s.store.Reserve(ctx, req)
	require.NoError(t, err)
	calls := 0
	dispatch := func(context.Context, domain.AIReservation) (domain.AISettleRequest, error) {
		calls++
		return serviceAIUsage(req.Model.ModelID), nil
	}
	check := func(want domain.AIOperationDisposition) {
		t.Helper()
		out, _, err := s.RunSuccess(ctx, req, dispatch)
		require.NoError(t, err)
		require.False(t, out.Created)
		require.Equal(t, want, out.Disposition)
		require.Equal(t, first.OperationID, out.OperationID)
		require.Zero(t, calls)
	}
	check(domain.AIOperationInProgress) // creator crashed before MarkSent
	claimed, err := s.store.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	require.True(t, claimed)
	check(domain.AIOperationInProgress) // dispatch already claimed
	_, err = s.Fail(ctx, domain.AIFailureRequest{OrganizationID: org, AttemptID: first.AttemptID, EventID: uuid.New(), Outcome: domain.AIFailureUnknown, Reason: "provider_error", EvidenceSource: "executor"})
	require.NoError(t, err)
	check(domain.AIOperationReconciliation)
	usage := serviceAIUsage(req.Model.ModelID)
	usage.OrganizationID, usage.AttemptID = org, first.AttemptID
	_, err = s.Complete(ctx, usage)
	require.NoError(t, err)
	check(domain.AIOperationStopped)
	changed := req
	changed.Fingerprint = sha256.Sum256([]byte("edited-user-message"))
	_, _, err = s.RunSuccess(ctx, changed, dispatch)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict)
	require.Zero(t, calls)
	// Equal text under a NEW user operation ID intentionally permits generation.
	newRequest := req
	newRequest.IdempotencyKey = "explicit-regenerate"
	out, _, err := s.RunSuccess(ctx, newRequest, dispatch)
	require.NoError(t, err)
	require.True(t, out.Created)
	require.NotEqual(t, first.OperationID, out.OperationID)
	require.Equal(t, 1, calls)
}

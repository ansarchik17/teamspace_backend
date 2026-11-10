package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func TestAIObserveUnknownUsageAndDisabledCompletion(t *testing.T) {
	ctx, db, _, org := newAIOperationServiceDB(t)
	for _, name := range []string{"20261020100800_ai_reservation_reconciliation.sql", "20261020101000_ai_budget_observations.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	_, err := db.Exec(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=0,max_five_hour_microcredits=0`)
	require.NoError(t, err)
	store := repository.NewAIReservationRepository(db, nil, repository.WithAIEnforcementMode(domain.AIEnforcementModeObserve), repository.WithAIUserConcurrencyLimit(1))
	executor := NewAIPaidCallerService(store)
	uid := int64(1)
	caller, err := domain.NewAIPaidUserCaller(org, uid)
	require.NoError(t, err)
	paidCtx := domain.WithAIPaidCaller(ctx, caller)
	calls := 0
	result, err := executor.ExecutePaidCall(paidCtx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return "provider answered without usage", domain.AISettleRequest{ActualModel: "openai/gpt-oss-120b", UsageAdapterVersion: "test-v1", UsageSource: "provider_usage", Usage: domain.AICreditUsage{Status: "unknown"}}, nil
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
	require.NotNil(t, result)
	var status, hold string
	var charge *int64
	require.NoError(t, db.QueryRow(ctx, `SELECT a.status,r.status,a.charged_microcredits FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id`).Scan(&status, &hold, &charge))
	require.Equal(t, "unknown", status)
	require.Equal(t, "active", hold)
	require.Nil(t, charge)
	store.SetEnforcementMode(domain.AIEnforcementModeEnforced)
	store.SetAccountingEnabled(false)
	params := validTestParams()
	params.IdempotencyKey = "disabled-next"
	_, err = executor.ExecutePaidCall(paidCtx, params, func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, err, domain.ErrAIAccountingDisabled)
	require.Equal(t, 1, calls)
	var active int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_reservations WHERE status='active'`).Scan(&active))
	require.Equal(t, 1, active)
	input, output := int64(1), int64(1)
	settlement, err := store.SettleSuccess(ctx, domain.AISettleRequest{OrganizationID: org, AttemptID: result.Reservation.AttemptID, ActualModel: "openai/gpt-oss-120b", UsageAdapterVersion: "test-v1", UsageSource: "provider_usage", Usage: domain.AICreditUsage{Status: "verified", InputTokens: &input, OutputTokens: &output}})
	require.NoError(t, err, "disabled admission must not disable settlement of existing work")
	require.Equal(t, int64(4000), settlement.ChargedMicrocredits)
	var actualExceeded bool
	require.NoError(t, db.QueryRow(ctx, `SELECT exceeded_with_actual_usage FROM ai_usage_budget_exceedances WHERE attempt_id=$1 AND scope='company'`, result.Reservation.AttemptID).Scan(&actualExceeded))
	require.True(t, actualExceeded)
}

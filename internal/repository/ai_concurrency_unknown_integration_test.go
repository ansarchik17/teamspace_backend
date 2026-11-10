package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func installAIConcurrencyReconciliationFixture(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
}

func TestAIUnknownOutcomeIsPendingConfirmationInsteadOfRunningGeneration(t *testing.T) {
	ctx, db, r, _, org, _, now := newAISettlementTestDB(t)
	installAIConcurrencyReconciliationFixture(t, ctx, db)
	user := int64(3)
	r.userConcurrencyLimit = 1
	first, err := r.Reserve(ctx, aiReserveRequest(org, &user, "unknown-first", 10_000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, first.AttemptID)
	require.NoError(t, err)
	_, err = r.Reserve(ctx, aiReserveRequest(org, &user, "while-running", 10_000))
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)
	_, err = r.FinishFailure(ctx, aiFailureRequest(org, first.AttemptID, domain.AIFailureUnknown, "provider_error"))
	require.NoError(t, err)
	otherInstance := NewAIReservationRepository(db, func() time.Time { return *now }, WithAIUserConcurrencyLimit(1))
	_, err = otherInstance.Reserve(ctx, aiReserveRequest(org, &user, "after-error", 10_000))
	denied := requireReserveDenied(t, err, "RESERVATION_PENDING")
	require.Equal(t, "account", denied.Scope)
	require.Nil(t, denied.AvailableMicrocredits, "not an estimate that may be resized and retried")
	var held int64
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits,status FROM ai_usage_reservations WHERE attempt_id=$1`, first.AttemptID).Scan(&held, &status))
	require.EqualValues(t, 10_000, held)
	require.Equal(t, "active", status, "unknown spending remains fully reserved")
	_, err = r.FinishFailure(ctx, aiFailureRequest(org, first.AttemptID, domain.AIFailureNotExecuted, "provider_error"))
	require.NoError(t, err, "only confirmed outcome evidence can release the unknown attempt")
	_, err = otherInstance.Reserve(ctx, aiReserveRequest(org, &user, "after-confirmation", 10_000))
	require.NoError(t, err)
}

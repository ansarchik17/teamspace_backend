package repository

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
	"time"
)

func TestAIPersonalPolicyUnknownReconciliationWithoutCompany(t *testing.T) {
	ctx, db, r, _ := newAIReconciliationTestDB(t)
	uid := int64(1)
	first, err := r.Reserve(ctx, aiReserveRequest(uuid.Nil, &uid, "personal-unknown", 10000))
	require.NoError(t, err)
	sent, err := r.MarkSent(ctx, uuid.Nil, first.AttemptID)
	require.NoError(t, err)
	require.True(t, sent)
	failed, err := r.FinishFailure(ctx, aiFailureRequest(uuid.Nil, first.AttemptID, domain.AIFailureUnknown, "timeout"))
	require.NoError(t, err)
	require.Equal(t, "unknown", failed.AttemptStatus)
	var held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM ai_usage_reservations WHERE attempt_id=$1 AND status='active'`, first.AttemptID).Scan(&held))
	require.EqualValues(t, 10000, held)
	dueAIReconciliation(t, ctx, db, r, first.AttemptID)
	job, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, uuid.Nil, job.OrganizationID)
	usage := aiSettleRequest(uuid.Nil, first.AttemptID, 2, 1)
	result, err := r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcileUsage, Usage: &usage}, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, "completed", result.JobStatus)
	var used int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits FROM ai_user_weekly_periods WHERE user_id=1`).Scan(&used))
	require.EqualValues(t, 5000, used)
	// A company identity cannot claim a no-company attempt.
	_, err = r.SettleSuccess(ctx, aiSettleRequest(uuid.New(), first.AttemptID, 2, 1))
	require.ErrorIs(t, err, domain.ErrAISettlementNotFound)
}

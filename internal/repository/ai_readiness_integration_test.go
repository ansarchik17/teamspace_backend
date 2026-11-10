package repository

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestAIReadinessAndManualResolution(t *testing.T) {
	ctx, db, r, org := newAIReconciliationTestDB(t)
	for _, file := range []string{"20261020101000_ai_budget_observations.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	_, err := db.Exec(ctx, `CREATE TABLE goose_db_version(id BIGSERIAL PRIMARY KEY,version_id BIGINT,is_applied BOOLEAN)`)
	require.NoError(t, err)
	for _, version := range domain.AIAccountingMigrationVersions {
		_, err = db.Exec(ctx, `INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true)`, version)
		require.NoError(t, err)
	}
	before, err := r.AIReadiness(ctx, time.Minute)
	require.NoError(t, err)
	require.False(t, before.ReadyForEnforcement())
	require.Positive(t, before.UnconfiguredCompanies)
	require.Zero(t, before.UnconfiguredEmployees, "missing personal overrides use defaults")
	var periods int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_weekly_periods`).Scan(&periods))
	require.Zero(t, periods, "preflight must not open periods")
	admin := NewOrganizationRepository(db, zap.NewNop())
	rows, err := db.Query(ctx, `SELECT id FROM organizations WHERE status='active' AND id<>$1`, org)
	require.NoError(t, err)
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		orgs = append(orgs, id)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	for _, id := range orgs {
		require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &id, aiBudgetRequest(100000, 100000, 0, 0)))
	}
	rows, err = db.Query(ctx, `SELECT u.id,u.organization_id,b.revision FROM users u JOIN organizations o ON o.id=u.organization_id AND o.status='active' JOIN organization_ai_budgets b ON b.organization_id=o.id LEFT JOIN organization_ai_user_quotas q ON q.user_id=u.id AND q.organization_id=o.id WHERE u.is_active AND u.deleted_at IS NULL AND q.weekly_quota_microcredits IS NULL`)
	require.NoError(t, err)
	type missing struct {
		user     int64
		org      uuid.UUID
		revision int64
	}
	var missingUsers []missing
	for rows.Next() {
		var m missing
		require.NoError(t, rows.Scan(&m.user, &m.org, &m.revision))
		missingUsers = append(missingUsers, m)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	for _, m := range missingUsers {
		require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 1, m.user, &m.org, aiUserQuotaRequest(0, 0, 0, m.revision)))
	}
	ready, err := r.AIReadiness(ctx, time.Minute)
	require.NoError(t, err)
	require.True(t, ready.ReadyForEnforcement())
	old := time.Now().UTC().Add(-time.Hour)
	r = NewAIReservationRepository(db, func() time.Time { return old })
	user := int64(3)
	reservation, err := r.Reserve(ctx, aiReserveRequest(org, &user, "operator-reconcile", 10000))
	require.NoError(t, err)
	_, err = r.MarkSent(ctx, org, reservation.AttemptID)
	require.NoError(t, err)
	dueAIReconciliation(t, ctx, db, r, reservation.AttemptID)
	job, err := r.ClaimReconciliation(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, job)
	_, err = r.ApplyReconciliation(ctx, *job, domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "lookup_unavailable"}, time.Minute, 10)
	require.NoError(t, err)
	held, err := r.AIReadiness(ctx, time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, held.UnknownReservations)
	require.False(t, held.ReadyForEnforcement())
	inspected, err := r.InspectAIAttempt(ctx, org, reservation.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "active", inspected.ReservationStatus)
	_, err = r.InspectAIAttempt(ctx, uuid.New(), reservation.AttemptID)
	require.Error(t, err)
	reference := "operator/test:ticket-42"
	usage := aiSettleRequest(org, reservation.AttemptID, 2, 1)
	usage.UsageSource = "manual_provider_confirmation"
	req := domain.AIFailureRequest{OrganizationID: org, AttemptID: reservation.AttemptID, EventID: uuid.NewSHA1(reservation.AttemptID, []byte(reference)), Outcome: domain.AIFailureCharged, Reason: "provider_error", EvidenceSource: "provider_confirmation", EvidenceReference: &reference, Usage: &usage}
	first, err := r.FinishFailure(ctx, req)
	require.NoError(t, err)
	require.True(t, first.Applied)
	replay, err := r.FinishFailure(ctx, req)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	_, err = r.ScheduleReconciliation(ctx, time.Minute, 25)
	require.NoError(t, err)
	after, err := r.AIReadiness(ctx, time.Minute)
	require.NoError(t, err)
	require.True(t, after.ReadyForEnforcement())
	require.Zero(t, after.ActiveReservations)
	var charged int64
	require.NoError(t, db.QueryRow(ctx, `SELECT charged_microcredits FROM ai_usage_attempts WHERE id=$1`, reservation.AttemptID).Scan(&charged))
	require.EqualValues(t, 5000, charged)
	// A later Down record must override a historical Up entry.
	_, err = db.Exec(ctx, `INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,false)`, domain.AIAccountingMigrationVersions[0])
	require.NoError(t, err)
	missingReport, err := r.AIReadiness(ctx, time.Minute)
	require.NoError(t, err)
	require.Equal(t, []int64{domain.AIAccountingMigrationVersions[0]}, missingReport.MissingMigrations)
}

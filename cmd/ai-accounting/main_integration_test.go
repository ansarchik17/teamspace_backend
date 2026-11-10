package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

// This fixture is deliberately opt-in and remains in a disposable, fully
// migrated DB so the release script can dump and restore real journal records.
func TestManualResolutionCLIRehearsal(t *testing.T) {
	if os.Getenv("TEST_AI_OPS_REHEARSAL") != "1" {
		t.Skip("release rehearsal only")
	}
	dsn := os.Getenv("AI_OPS_DATABASE_URL")
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	require.Equal(t, "ai_budget_rehearsal", cfg.ConnConfig.Database, "never run fixtures against an arbitrary database")
	ctx := context.Background()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	var role string
	require.NoError(t, db.QueryRow(ctx, `SELECT role FROM users WHERE id=1`).Scan(&role))
	require.Equal(t, "admin", role)
	org := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_id) VALUES($1,'AI rehearsal',$2,1)`, org, "ai-rehearsal-"+org.String())
	require.NoError(t, err)
	var user int64
	require.NoError(t, db.QueryRow(ctx, `INSERT INTO users(id,email,username,first_name,last_name,organization_id) SELECT COALESCE(max(id),0)+1,$1,$2,'Synthetic','Employee',$3 FROM users RETURNING id`, org.String()+"@example.invalid", org.String(), org).Scan(&user))
	admin := repository.NewOrganizationRepository(db, zap.NewNop())
	value := func(n int64) *int64 { return &n }
	require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &org, domain.SetAIBudgetRequest{WeeklyBudget: value(1000000), MaxFiveHour: value(100000), ServiceQuota: value(0), Revision: value(0)}))
	require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 1, user, &org, domain.SetAIUserQuotaRequest{WeeklyQuota: value(100000), FiveHourLimit: value(100000), Revision: value(0), CompanyRevision: value(1)}))
	repo := repository.NewAIReservationRepository(db, nil)
	accounting := service.NewAIAccountingService(repo)
	reserve := func(key string) domain.AIReservation {
		request := domain.AIReserveRequest{OrganizationID: org, UserID: &user, OperationKind: "chat", IdempotencyKey: key, Fingerprint: sha256.Sum256([]byte(key)), AttemptFingerprint: sha256.Sum256([]byte(key + ":payload")), AttemptNumber: 1, Model: domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: "openai/gpt-oss-120b", PolicyVersion: domain.AICreditPolicyV1}, UsageAdapterVersion: "rehearsal-v1", Microcredits: 10000}
		r, e := repo.Reserve(ctx, request)
		require.NoError(t, e)
		claimed, e := repo.MarkSent(ctx, org, r.AttemptID)
		require.NoError(t, e)
		require.True(t, claimed)
		_, e = accounting.Fail(ctx, domain.AIFailureRequest{OrganizationID: org, AttemptID: r.AttemptID, EventID: uuid.New(), Outcome: domain.AIFailureUnknown, Reason: "timeout", EvidenceSource: "executor"})
		require.NoError(t, e)
		return r
	}
	first := reserve("rehearsal-confirmed-" + org.String())
	evidence := resolutionEvidence{Organization: org, Attempt: first.AttemptID, Operator: "test-operator", Reference: "synthetic-confirmation", ProviderConfirmed: true, Outcome: "usage", Provider: "nitec", Endpoint: "chat", Model: "openai/gpt-oss-120b", Adapter: "rehearsal-v1", Usage: &manualUsage{Input: value(2), Output: value(1), Total: value(3)}}
	encoded, err := json.Marshal(evidence)
	require.NoError(t, err)
	file := os.Getenv("TEST_AI_OPS_EVIDENCE_FILE")
	if file == "" {
		file = filepath.Join(t.TempDir(), "evidence.json")
	}
	require.NoError(t, os.WriteFile(file, encoded, 0600))
	args := []string{"resolve", "--database-name", "ai_budget_rehearsal", "--evidence", file}
	require.NoError(t, run(args, &bytes.Buffer{}))
	inspection, err := repo.InspectAIAttempt(ctx, org, first.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "active", inspection.ReservationStatus, "preview must not change holds")
	for i := 0; i < 2; i++ {
		require.NoError(t, run(append(args, "--apply"), &bytes.Buffer{}))
	}
	var charged int64
	require.NoError(t, db.QueryRow(ctx, `SELECT charged_microcredits FROM ai_usage_attempts WHERE id=$1`, first.AttemptID).Scan(&charged))
	require.EqualValues(t, 5000, charged)
	reserve("rehearsal-unknown-" + org.String())
	report, err := repo.AIReadiness(ctx, 15*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, report.UnknownReservations)
	require.False(t, report.ReadyForEnforcement())
	var output bytes.Buffer
	require.Error(t, run([]string{"preflight", "--database-name", "ai_budget_rehearsal"}, &output))
	require.Contains(t, output.String(), `"unknown_reservations":1`)
	t.Run("initial quotas preserve explicit settings and audit", func(t *testing.T) {
		company := uuid.New()
		_, err := db.Exec(ctx, `INSERT INTO organizations(id,name,slug) VALUES($1,'Defaults rehearsal',$2)`, company, "defaults-"+company.String())
		require.NoError(t, err)
		createEmployee := func() int64 {
			var id int64
			identity := uuid.NewString()
			require.NoError(t, db.QueryRow(ctx, `INSERT INTO users(id,email,username,first_name,last_name,organization_id) SELECT COALESCE(max(id),0)+1,$1,$2,'Defaults','Fixture',$3 FROM users RETURNING id`, identity+"@example.invalid", identity, company).Scan(&id))
			return id
		}
		pending, disabled, blocked := createEmployee(), createEmployee(), createEmployee()
		_, err = db.Exec(ctx, `INSERT INTO organization_user_blocks(user_id,organization_id,actor_id) VALUES($1,$2,1)`, blocked, company)
		require.NoError(t, err)
		require.Error(t, initializeDefaults(ctx, db, 1, company, true, &bytes.Buffer{}), "missing company budget must not create quotas")
		require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &company, domain.SetAIBudgetRequest{WeeklyBudget: value(50_000_000), MaxFiveHour: value(20_000_000), ServiceQuota: value(0), Revision: value(0)}))
		require.NoError(t, admin.SetAdministrationAIUserQuota(ctx, 1, disabled, &company, domain.SetAIUserQuotaRequest{WeeklyQuota: value(0), FiveHourLimit: value(0), Revision: value(0), CompanyRevision: value(1)}))
		require.Error(t, initializeDefaults(ctx, db, pending, company, false, &bytes.Buffer{}), "employee cannot impersonate platform admin")
		require.Error(t, initializeDefaults(ctx, db, 1, company, true, &bytes.Buffer{}), "insufficient budget must leave settings unchanged")
		budget, err := admin.AdministrationAIBudget(ctx, 1, &company)
		require.NoError(t, err)
		require.NoError(t, admin.SetAdministrationAIBudget(ctx, 1, &company, domain.SetAIBudgetRequest{WeeklyBudget: value(300_000_000), MaxFiveHour: value(20_000_000), ServiceQuota: value(0), Revision: &budget.Revision}))
		require.NoError(t, initializeDefaults(ctx, db, 1, company, false, &bytes.Buffer{}))
		var count int
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_user_quotas WHERE organization_id=$1 AND weekly_quota_microcredits IS NOT NULL`, company).Scan(&count))
		require.Equal(t, 1, count, "preview must not write")
		require.NoError(t, initializeDefaults(ctx, db, 1, company, true, &bytes.Buffer{}))
		var weekly, five, revision int64
		require.NoError(t, db.QueryRow(ctx, `SELECT weekly_quota_microcredits,five_hour_limit_microcredits,revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2`, company, pending).Scan(&weekly, &five, &revision))
		require.Equal(t, initialWeeklyQuota, weekly)
		require.Equal(t, initialFiveHourLimit, five)
		require.NoError(t, initializeDefaults(ctx, db, 1, company, true, &bytes.Buffer{}))
		var replayRevision int64
		require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2`, company, pending).Scan(&replayRevision))
		require.Equal(t, revision, replayRevision, "replay must not change existing settings")
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE organization_id=$1 AND object_id=$2 AND action='ai.user_quota.update'`, company, fmt.Sprint(pending)).Scan(&count))
		require.Equal(t, 1, count, "defaults and replay must produce one audited assignment")
		require.NoError(t, db.QueryRow(ctx, `SELECT weekly_quota_microcredits FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2`, company, disabled).Scan(&weekly))
		require.Zero(t, weekly, "explicit zero is preserved")
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_user_quotas WHERE organization_id=$1 AND user_id=$2 AND weekly_quota_microcredits IS NOT NULL`, company, blocked).Scan(&count))
		require.Zero(t, count, "blocked employee is not allocated credits")
	})
}

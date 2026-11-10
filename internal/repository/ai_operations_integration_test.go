package repository

import (
	"crypto/sha256"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func aiOperationLookup(req domain.AIReserveRequest) domain.AIOperationLookup {
	return domain.AIOperationLookup{OrganizationID: req.OrganizationID, UserID: req.UserID, IdempotencyKey: req.IdempotencyKey, Fingerprint: req.Fingerprint, OperationKind: req.OperationKind, SourceKind: req.SourceKind, SourceID: req.SourceID}
}

func TestAIOperationLookupAndAttemptIdentity(t *testing.T) {
	ctx, db, r, admin, a, b, _ := newAISettlementTestDB(t)
	uid := int64(3)
	req := aiReserveRequest(a, &uid, "request-identity", 10_000)
	missing, err := r.FindOperation(ctx, aiOperationLookup(req))
	require.NoError(t, err)
	require.Nil(t, missing)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_weekly_anchors`).Scan(&count))
	require.Zero(t, count, "lookup cannot open accounting windows")
	first, err := r.Reserve(ctx, req)
	require.NoError(t, err)
	changed := req
	changed.AttemptFingerprint = sha256.Sum256([]byte("changed-full-provider-request"))
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict)
	changed.AttemptFingerprint = [32]byte{}
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, domain.ErrAIReservationInvalid)
	view, err := r.FindOperation(ctx, aiOperationLookup(req))
	require.NoError(t, err)
	require.Equal(t, 1, view.AttemptCount)
	require.Equal(t, first.AttemptID, view.LatestAttempt.AttemptID)
	require.Equal(t, domain.AIOperationInProgress, view.Disposition)
	otherUser := int64(2)
	lookup := aiOperationLookup(req)
	lookup.UserID = &otherUser
	view, err = r.FindOperation(ctx, lookup)
	require.NoError(t, err)
	require.Nil(t, view, "same key belongs to different users independently")
	lookup = aiOperationLookup(req)
	lookup.UserID = nil
	view, err = r.FindOperation(ctx, lookup)
	require.NoError(t, err)
	require.Nil(t, view, "company service keys cannot match user keys")
	_, err = db.Exec(ctx, `UPDATE ai_usage_attempts SET request_fingerprint=$2 WHERE id=$1`, first.AttemptID, changed.AttemptFingerprint[:])
	require.Error(t, err, "the attempt payload digest is immutable")
	require.NoError(t, admin.SetEmployeeBlocked(ctx, 2, 3, nil, true))
	_, err = r.FindOperation(ctx, aiOperationLookup(req))
	requireReserveDenied(t, err, "AI_ACCESS_DENIED")
	require.NoError(t, admin.SetEmployeeBlocked(ctx, 2, 3, nil, false))
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	lookup = aiOperationLookup(req)
	lookup.OrganizationID = b
	_, err = r.FindOperation(ctx, lookup)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict, "transfer cannot recycle an existing user operation key")
}

func TestAIOperationHistoricalPolicyReplay(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	uid := int64(3)
	seed, err := r.Reserve(ctx, aiReserveRequest(a, &uid, "seed", 10_000))
	require.NoError(t, err)
	req := aiReserveRequest(a, &uid, "old-policy", 10_000)
	req.Model.PolicyVersion = "retired-policy"
	op, attempt := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_operations(id,organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint) VALUES($1,$2,3,'user','chat',$3,$4)`, op, a, req.IdempotencyKey, req.Fingerprint[:])
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,output_weight_microcredits,company_period_id,five_hour_period_id,request_fingerprint) VALUES($1,$2,$3,1,'nitec','chat','openai/gpt-oss-120b','retired-policy','test-v1',7,11,$4,$5,$6)`, attempt, op, a, seed.CompanyPeriodID, seed.FiveHourPeriodID, req.AttemptFingerprint[:])
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,10000)`, attempt)
	require.NoError(t, err)
	out, err := r.Reserve(ctx, req)
	require.NoError(t, err, "replay must not reprice the attempt using today's registry")
	require.False(t, out.Created)
	require.Equal(t, attempt, out.AttemptID)
}

func TestAIOperationRequestIdentityMigration(t *testing.T) {
	ctx, db, r, _, a, _, _ := newAISettlementTestDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100700_ai_attempt_request_identity.sql")
	require.NoError(t, err)
	parts := strings.Split(string(data), "-- +goose Down")
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err)
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	uid := int64(3)
	req := aiReserveRequest(a, &uid, "migration-identity", 10_000)
	first, err := r.Reserve(ctx, req)
	require.NoError(t, err)
	// A pre-migration attempt with unknown payload can be inspected, not resent.
	legacy := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,output_weight_microcredits,company_period_id,five_hour_period_id) VALUES($1,$2,$3,2,'nitec','chat','openai/gpt-oss-120b','credits-v1','test-v1',1000,3000,$4,$5)`, legacy, first.OperationID, a, first.CompanyPeriodID, first.FiveHourPeriodID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO ai_usage_reservations(attempt_id,reserved_microcredits) VALUES($1,1000)`, legacy)
	require.NoError(t, err)
	claimed, err := r.MarkSent(ctx, a, legacy)
	require.NoError(t, err)
	require.False(t, claimed)
	view, err := r.FindOperation(ctx, aiOperationLookup(req))
	require.NoError(t, err)
	require.Equal(t, legacy, view.LatestAttempt.AttemptID)
	require.False(t, view.LatestAttempt.Created)
	_, err = db.Exec(ctx, parts[1])
	require.Error(t, err, "rollback must preserve recorded request digests")
}

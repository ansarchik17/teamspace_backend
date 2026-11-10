package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func manualFixture(t *testing.T) (resolutionEvidence, domain.AIReconciliationJob) {
	t.Helper()
	data := `{"organization_id":"11111111-1111-4111-8111-111111111111","attempt_id":"22222222-2222-4222-8222-222222222222","operator":"operator-1","evidence_reference":"ticket-42","provider_confirmed":true,"outcome":"usage","provider_id":"nitec","endpoint_kind":"chat","actual_model":"openai/gpt-oss-120b","usage_adapter_version":"test-v1","usage":{"input_tokens":10,"output_tokens":2}}`
	e, err := readEvidence(strings.NewReader(data))
	require.NoError(t, err)
	job := domain.AIReconciliationJob{OrganizationID: e.Organization, AttemptID: e.Attempt, ProviderID: e.Provider, EndpointKind: e.Endpoint, UsageAdapterVersion: e.Adapter, PolicyVersion: domain.AICreditPolicyV1}
	return e, job
}

func TestManualEvidenceBoundToAttempt(t *testing.T) {
	e, job := manualFixture(t)
	first, err := e.failure(job)
	require.NoError(t, err)
	second, err := e.failure(job)
	require.NoError(t, err)
	require.Equal(t, first, second, "identical evidence has a stable event and fingerprint")
	require.Equal(t, domain.AIFailureCharged, first.Outcome)
	require.Contains(t, *first.EvidenceReference, "operator-1")
	job.OrganizationID = uuid.New()
	_, err = e.failure(job)
	require.Error(t, err)
	job.OrganizationID = e.Organization
	job.UsageAdapterVersion = "other"
	_, err = e.failure(job)
	require.Error(t, err)
	job.UsageAdapterVersion = e.Adapter
	requestID := "recorded-request"
	job.ProviderRequestID = &requestID
	_, err = e.failure(job)
	require.Error(t, err)
	e.RequestID = &requestID
	_, err = e.failure(job)
	require.NoError(t, err)
}

func TestManualEvidenceRejectsUnknownAndGuessedUsage(t *testing.T) {
	for _, data := range []string{`{}`, `null`, `{"outcome":"unknown"}`, `{"unexpected":"field"}`, `{} {}`, `{"usage":{"input_tokens":0.1}}`} {
		_, err := readEvidence(strings.NewReader(data))
		require.Error(t, err)
	}
	e, job := manualFixture(t)
	e.Usage.Input = nil
	_, err := e.failure(job)
	require.Error(t, err)
	e, job = manualFixture(t)
	negative := int64(-1)
	e.Usage.Output = &negative
	_, err = e.failure(job)
	require.Error(t, err)
	e, job = manualFixture(t)
	e.Model = "unpriced"
	_, err = e.failure(job)
	require.Error(t, err)
	e, job = manualFixture(t)
	e.Outcome = "not_executed"
	e.Usage = nil
	req, err := e.failure(job)
	require.NoError(t, err)
	require.Nil(t, req.Usage)
	require.Equal(t, domain.AIFailureNotExecuted, req.Outcome)
}

func TestManualCommandRequiresExplicitTarget(t *testing.T) {
	for _, args := range [][]string{nil, {"delete"}, {"preflight"}, {"resolve", "--database-name", "test"}} {
		require.Error(t, run(args, &bytes.Buffer{}))
	}
	require.ErrorContains(t, run([]string{"preflight", "--database-name", "test", "--apply"}, &bytes.Buffer{}), "flags do not match")
	require.ErrorContains(t, run([]string{"resolve", "--database-name", "test", "--organization", uuid.NewString()}, &bytes.Buffer{}), "only valid for inspect")
	t.Setenv("AI_OPS_DATABASE_URL", "postgres://local:local@localhost/other")
	require.ErrorContains(t, run([]string{"preflight", "--database-name", "expected"}, &bytes.Buffer{}), "mismatch")
}

package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIAudioSettlementIntegration(t *testing.T) {
	ctx, db, repo, _, org, _, _ := newAISettlementTestDB(t)
	migration, err := os.ReadFile("../../db/migrations/20261022100600_ai_audio_measurements.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	uid := int64(3)
	request := aiReserveRequest(org, &uid, "audio-one", 60_000)
	request.OperationKind = "stt"
	request.Model = domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "stt", ModelID: "openai/whisper-large-v3-turbo", PolicyVersion: domain.AIAudioCreditPolicyV1}
	request.UsageAdapterVersion = "workspace-stt-v1"
	reservation, err := repo.Reserve(ctx, request)
	require.NoError(t, err)
	quantity := int64(2)
	evidence := domain.AISettleRequest{OrganizationID: org, AttemptID: reservation.AttemptID, ActualModel: request.Model.ModelID, UsageAdapterVersion: request.UsageAdapterVersion,
		UsageSource: "gateway_audio_probe", Usage: domain.AICreditUsage{Status: "derived", MeasuredUnit: "audio_seconds", MeasuredQuantity: &quantity}}
	settlement, err := repo.SettleSuccess(ctx, evidence)
	require.NoError(t, err)
	require.EqualValues(t, 40_000, settlement.ChargedMicrocredits)
	require.EqualValues(t, 20_000, settlement.ReleasedMicrocredits)
	var unit string
	var recorded int64
	var input, output *int64
	err = db.QueryRow(ctx, `SELECT measured_unit,measured_quantity,input_tokens,output_tokens FROM ai_usage_attempts WHERE id=$1`, reservation.AttemptID).Scan(&unit, &recorded, &input, &output)
	require.NoError(t, err)
	require.Equal(t, "audio_seconds", unit)
	require.EqualValues(t, 2, recorded)
	require.Nil(t, input)
	require.Nil(t, output)
	replay, err := repo.SettleSuccess(ctx, evidence)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	_, err = db.Exec(ctx, `UPDATE ai_usage_attempts SET measured_quantity=100 WHERE id=$1`, reservation.AttemptID)
	require.Error(t, err, "settled measured usage must be immutable")
	text, err := repo.Reserve(ctx, aiReserveRequest(org, &uid, "text-after-audio", 5_000))
	require.NoError(t, err)
	_, err = repo.SettleSuccess(ctx, aiSettleRequest(org, text.AttemptID, 2, 1))
	require.NoError(t, err, "existing token accounting still works")
}

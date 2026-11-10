package domain

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAIPaidCallerValidation(t *testing.T) {
	org := uuid.New()
	userID := int64(42)

	// User caller valid
	userCaller, err := NewAIPaidUserCaller(org, userID)
	require.NoError(t, err)
	require.False(t, userCaller.IsService)
	require.Equal(t, org, userCaller.OrganizationID)
	require.Equal(t, &userID, userCaller.UserID)

	// A personal caller does not need a company.
	_, err = NewAIPaidUserCaller(uuid.Nil, userID)
	require.NoError(t, err)

	// User caller invalid userID <= 0
	_, err = NewAIPaidUserCaller(org, 0)
	require.ErrorIs(t, err, ErrAIPaidCallInvalidCaller)
	_, err = NewAIPaidUserCaller(org, -1)
	require.ErrorIs(t, err, ErrAIPaidCallInvalidCaller)

	// Service caller valid
	serviceCaller, err := NewAIPaidServiceCaller(org)
	require.NoError(t, err)
	require.True(t, serviceCaller.IsService)
	require.Nil(t, serviceCaller.UserID)

	// Service caller invalid org
	_, err = NewAIPaidServiceCaller(uuid.Nil)
	require.ErrorIs(t, err, ErrAIPaidCallInvalidCaller)

	// Direct invalid struct
	require.ErrorIs(t, AIPaidCaller{OrganizationID: org, IsService: true, UserID: &userID}.Validate(), ErrAIPaidCallInvalidCaller)
	require.ErrorIs(t, AIPaidCaller{OrganizationID: org, IsService: false, UserID: nil}.Validate(), ErrAIPaidCallInvalidCaller)
}

func TestAIPaidCallerContext(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	caller, _ := NewAIPaidUserCaller(org, userID)

	// Context with valid caller
	ctx := WithAIPaidCaller(context.Background(), caller)
	retrieved, ok := AIPaidCallerFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, caller, retrieved)

	// Nil context or missing
	_, ok = AIPaidCallerFromContext(nil)
	require.False(t, ok)
	_, ok = AIPaidCallerFromContext(context.Background())
	require.False(t, ok)

	// Invalid caller in context
	badCtx := WithAIPaidCaller(context.Background(), AIPaidCaller{})
	_, ok = AIPaidCallerFromContext(badCtx)
	require.False(t, ok)
}

func TestAIPaidCallParamsResolveCaller(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	caller, _ := NewAIPaidUserCaller(org, userID)

	// Missing context and params caller
	params := AIPaidCallParams{}
	_, err := params.ResolveCaller(context.Background())
	require.ErrorIs(t, err, ErrAIPaidCallMissingContext)

	// Context caller only
	ctx := WithAIPaidCaller(context.Background(), caller)
	resolved, err := params.ResolveCaller(ctx)
	require.NoError(t, err)
	require.Equal(t, caller, resolved)

	// Params caller only
	paramsWithCaller := AIPaidCallParams{Caller: caller}
	resolved, err = paramsWithCaller.ResolveCaller(context.Background())
	require.NoError(t, err)
	require.Equal(t, caller, resolved)

	// Both matching
	resolved, err = paramsWithCaller.ResolveCaller(ctx)
	require.NoError(t, err)
	require.Equal(t, caller, resolved)

	// Mismatched org
	otherOrg := uuid.New()
	mismatchedOrgCaller, _ := NewAIPaidUserCaller(otherOrg, userID)
	paramsMismatch := AIPaidCallParams{Caller: mismatchedOrgCaller}
	_, err = paramsMismatch.ResolveCaller(ctx)
	require.ErrorIs(t, err, ErrAIPaidCallContextMismatch)

	// Mismatched user
	mismatchedUserCaller, _ := NewAIPaidUserCaller(org, 99)
	paramsMismatchUser := AIPaidCallParams{Caller: mismatchedUserCaller}
	_, err = paramsMismatchUser.ResolveCaller(ctx)
	require.ErrorIs(t, err, ErrAIPaidCallContextMismatch)
}

func TestAIPaidCallParamsToReserveRequest(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	caller, _ := NewAIPaidUserCaller(org, userID)

	fingerprint := [32]byte{1, 2, 3}
	attemptFingerprint := [32]byte{4, 5, 6}

	validParams := AIPaidCallParams{
		IdempotencyKey:      "op-key-1",
		OperationKind:       "chat",
		Model:               AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: "openai/gpt-oss-120b", PolicyVersion: AICreditPolicyV1},
		UsageAdapterVersion: "v1",
		Microcredits:        1000,
		Fingerprint:         fingerprint,
		AttemptFingerprint:  attemptFingerprint,
		AttemptNumber:       1,
	}

	req, err := validParams.ToReserveRequest(caller)
	require.NoError(t, err)
	require.Equal(t, org, req.OrganizationID)
	require.Equal(t, &userID, req.UserID)
	require.Equal(t, "chat", req.OperationKind)
	require.Equal(t, "op-key-1", req.IdempotencyKey)
	require.Equal(t, fingerprint, req.Fingerprint)
	require.Equal(t, attemptFingerprint, req.AttemptFingerprint)
	require.Equal(t, int32(1), req.AttemptNumber)
	require.Equal(t, int64(1000), req.Microcredits)

	// Invalid fields
	invalidParams := validParams
	invalidParams.IdempotencyKey = ""
	_, err = invalidParams.ToReserveRequest(caller)
	require.ErrorIs(t, err, ErrAIPaidCallInvalidRequest)

	invalidParams = validParams
	invalidParams.Microcredits = 0
	_, err = invalidParams.ToReserveRequest(caller)
	require.ErrorIs(t, err, ErrAIPaidCallInvalidRequest)

	invalidParams = validParams
	invalidParams.Fingerprint = [32]byte{}
	_, err = invalidParams.ToReserveRequest(caller)
	require.ErrorIs(t, err, ErrAIPaidCallInvalidRequest)
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func validTestParams() domain.AIPaidCallParams {
	return domain.AIPaidCallParams{
		IdempotencyKey:      "op-key-test-1",
		OperationKind:       "chat",
		Model:               domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: "openai/gpt-oss-120b", PolicyVersion: domain.AICreditPolicyV1},
		UsageAdapterVersion: "test-v1",
		Microcredits:        5000,
		Fingerprint:         [32]byte{1, 2, 3},
		AttemptFingerprint:  [32]byte{4, 5, 6},
		AttemptNumber:       1,
	}
}

func TestAIPaidCallMissingContext(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	svc := NewAIPaidCallerService(store)

	// 1. Completely empty context and no Caller in params: MUST fail and MUST NOT invoke dispatch!
	calls := 0
	_, err := svc.ExecutePaidCall(context.Background(), validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return "should not be called", domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, err, domain.ErrAIPaidCallMissingContext)
	require.Zero(t, calls, "dispatch must not be invoked when context is missing")
	require.Zero(t, store.reserveCalls)
	require.Zero(t, store.markCalls)

	// 2. A service caller still requires a company.
	invalidOrgCtx := domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{OrganizationID: uuid.Nil, IsService: true})
	_, err = svc.ExecutePaidCall(invalidOrgCtx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})
	require.Error(t, err)
	require.Zero(t, calls)

	// 3. User caller missing UserID
	invalidUserCtx := domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{OrganizationID: org, IsService: false, UserID: nil})
	_, err = svc.ExecutePaidCall(invalidUserCtx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	require.Zero(t, calls)

	// 4. Context caller and Param caller mismatch
	otherOrg := uuid.New()
	ctxCaller, _ := domain.NewAIPaidUserCaller(org, userID)
	paramCaller, _ := domain.NewAIPaidUserCaller(otherOrg, userID)
	validCtx := domain.WithAIPaidCaller(context.Background(), ctxCaller)

	mismatchParams := validTestParams()
	mismatchParams.Caller = paramCaller
	_, err = svc.ExecutePaidCall(validCtx, mismatchParams, func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, err, domain.ErrAIPaidCallContextMismatch)
	require.Zero(t, calls)

	// 5. Nil dispatch callback
	_, err = svc.ExecutePaidCall(validCtx, validTestParams(), nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidRequest)
}

func TestAIPaidCallSuccessfulExecution(t *testing.T) {
	org := uuid.New()
	userID := int64(42)
	attemptID := uuid.New()
	opID := uuid.New()

	store := &aiAccountingStub{
		reservation: domain.AIReservation{
			OperationID: opID,
			AttemptID:   attemptID,
			Created:     true,
		},
	}
	svc := NewAIPaidCallerService(store)

	caller, err := domain.NewAIPaidUserCaller(org, userID)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	inTokens, outTokens := int64(100), int64(50)
	evidence := domain.AISettleRequest{
		ActualModel:         "openai/gpt-oss-120b",
		UsageAdapterVersion: "test-v1",
		UsageSource:         "provider_api",
		Usage: domain.AICreditUsage{
			Status:       "verified",
			InputTokens:  &inTokens,
			OutputTokens: &outTokens,
		},
	}

	calls := 0
	result, err := svc.ExecutePaidCall(ctx, validTestParams(), func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		require.Equal(t, attemptID, reservation.AttemptID)
		return "test response payload", evidence, nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, store.reserveCalls)
	require.Equal(t, 1, store.markCalls)
	require.Len(t, store.completed, 1)
	require.Equal(t, org, store.completed[0].OrganizationID)
	require.Equal(t, attemptID, store.completed[0].AttemptID)
	require.Equal(t, "test response payload", result.Response)
	require.Equal(t, "settled", result.Reservation.AttemptStatus)
	require.NotNil(t, result.Settlement)
	require.True(t, result.Settlement.Applied)
}

func TestAIPaidCallServiceCaller(t *testing.T) {
	org := uuid.New()
	attemptID := uuid.New()
	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
	}
	svc := NewAIPaidCallerService(store)

	caller, err := domain.NewAIPaidServiceCaller(org)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	calls := 0
	result, err := svc.ExecutePaidCall(ctx, validTestParams(), func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return 12345, domain.AISettleRequest{Usage: domain.AICreditUsage{Status: "verified"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 12345, result.Response)
	require.NotNil(t, result.Settlement)
}

func TestAIPaidCallTypedHelper(t *testing.T) {
	org := uuid.New()
	userID := int64(77)
	attemptID := uuid.New()
	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
	}
	accountingSvc := NewAIAccountingService(store)

	caller, _ := domain.NewAIPaidUserCaller(org, userID)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	type CustomResponse struct {
		Answer string
	}

	resp, reservation, settlement, err := ExecutePaidCallTyped(ctx, accountingSvc, validTestParams(), func(ctx context.Context, res domain.AIReservation) (CustomResponse, domain.AISettleRequest, error) {
		return CustomResponse{Answer: "unified AI caller working"}, domain.AISettleRequest{Usage: domain.AICreditUsage{Status: "verified"}}, nil
	})

	require.NoError(t, err)
	require.Equal(t, "unified AI caller working", resp.Answer)
	require.Equal(t, attemptID, reservation.AttemptID)
	require.NotNil(t, settlement)
}

func TestAIPaidCallIdempotentReplay(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	attemptID := uuid.New()
	storedSettlement := &domain.AISettlement{AttemptID: attemptID, ChargedMicrocredits: 3500}

	store := &aiAccountingStub{
		reservation: domain.AIReservation{
			AttemptID:  attemptID,
			Created:    false, // Replay!
			Settlement: storedSettlement,
		},
	}
	svc := NewAIPaidCallerService(store)

	caller, _ := domain.NewAIPaidUserCaller(org, userID)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	calls := 0
	result, err := svc.ExecutePaidCall(ctx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return "should not be called on replay", domain.AISettleRequest{}, nil
	})

	require.NoError(t, err)
	require.Zero(t, calls, "dispatch must not be called on replay")
	require.Zero(t, store.markCalls)
	require.Equal(t, storedSettlement, result.Settlement)
}

func TestAIPaidCallCancelledBeforeDispatch(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	attemptID := uuid.New()

	ctx, cancel := context.WithCancel(context.Background())
	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
		onReserve:   cancel, // cancel context right after reservation
	}
	svc := NewAIPaidCallerService(store)

	caller, _ := domain.NewAIPaidUserCaller(org, userID)
	reqCtx := domain.WithAIPaidCaller(ctx, caller)

	calls := 0
	_, err := svc.ExecutePaidCall(reqCtx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
	require.Zero(t, store.markCalls)
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureNotDispatched, store.failures[0].Outcome)
}

func TestAIPaidCallProviderError(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	attemptID := uuid.New()
	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
	}
	svc := NewAIPaidCallerService(store)

	caller, _ := domain.NewAIPaidUserCaller(org, userID)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	providerErr := errors.New("upstream provider timeout")
	calls := 0
	result, err := svc.ExecutePaidCall(ctx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return "partial response", domain.AISettleRequest{}, providerErr
	})

	require.ErrorIs(t, err, providerErr)
	require.NotNil(t, result)
	require.Equal(t, "partial response", result.Response)
	require.Equal(t, attemptID, result.Reservation.AttemptID)
	require.Nil(t, result.Settlement)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, store.markCalls)
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)
}

func TestAIPaidCallReservationDenied(t *testing.T) {
	org := uuid.New()
	userID := int64(10)
	deniedErr := &domain.AIReservationDenied{Reason: "COMPANY_BUDGET_EXHAUSTED", Scope: "company"}

	store := &aiAccountingStub{
		reserveErr: deniedErr,
	}
	svc := NewAIPaidCallerService(store)

	caller, _ := domain.NewAIPaidUserCaller(org, userID)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)

	calls := 0
	_, err := svc.ExecutePaidCall(ctx, validTestParams(), func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		calls++
		return nil, domain.AISettleRequest{}, nil
	})

	require.ErrorIs(t, err, deniedErr)
	require.Zero(t, calls)
	require.Zero(t, store.markCalls)
}

func TestAIPaidCallRejectsInvalidIdentityWithoutFallback(t *testing.T) {
	caller, err := domain.NewAIPaidUserCaller(uuid.New(), 42)
	require.NoError(t, err)
	invalid := domain.AIPaidCaller{OrganizationID: uuid.New()}
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		caller domain.AIPaidCaller
	}{
		{"invalid params with valid context", domain.WithAIPaidCaller(context.Background(), caller), invalid},
		{"invalid context with valid params", domain.WithAIPaidCaller(context.Background(), invalid), caller},
		{"explicit zero context with valid params", domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{}), caller},
		{"invalid params without context", context.Background(), invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &aiAccountingStub{}
			params := validTestParams()
			params.Caller = tc.caller
			calls := 0
			result, err := NewAIPaidCallerService(store).ExecutePaidCall(tc.ctx, params, func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
				calls++
				return nil, domain.AISettleRequest{}, nil
			})
			require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
			require.Nil(t, result)
			require.Zero(t, calls)
			require.Zero(t, store.reserveCalls)
			require.Zero(t, store.markCalls)
		})
	}
}

func TestAIPaidCallPreservesResultOnSettlementFailure(t *testing.T) {
	for _, typed := range []bool{false, true} {
		name := "untyped"
		if typed {
			name = "typed"
		}
		t.Run(name, func(t *testing.T) {
			settleErr := errors.New("settlement unavailable")
			attemptID, operationID := uuid.New(), uuid.New()
			store := &aiAccountingStub{
				reservation: domain.AIReservation{AttemptID: attemptID, OperationID: operationID, Created: true},
				settleErr:   settleErr,
			}
			caller, err := domain.NewAIPaidUserCaller(uuid.New(), 42)
			require.NoError(t, err)
			ctx := domain.WithAIPaidCaller(context.Background(), caller)
			calls := 0
			dispatch := func(context.Context, domain.AIReservation) (string, domain.AISettleRequest, error) {
				calls++
				return "paid response", domain.AISettleRequest{}, nil
			}
			svc := NewAIPaidCallerService(store)
			var response any
			var reservation domain.AIReservation
			var settlement *domain.AISettlement
			if typed {
				response, reservation, settlement, err = ExecutePaidCallTyped(ctx, svc, validTestParams(), dispatch)
			} else {
				var result *AIPaidExecutionResult
				result, err = svc.ExecutePaidCall(ctx, validTestParams(), func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
					return dispatch(ctx, res)
				})
				require.NotNil(t, result)
				response, reservation, settlement = result.Response, result.Reservation, result.Settlement
			}
			require.ErrorIs(t, err, settleErr)
			require.Equal(t, "paid response", response)
			require.Equal(t, attemptID, reservation.AttemptID)
			require.Equal(t, operationID, reservation.OperationID)
			require.Equal(t, "sent", reservation.AttemptStatus)
			require.Nil(t, settlement)
			require.Equal(t, 1, calls)
			require.Len(t, store.completed, 1)
			require.Len(t, store.failures, 1)
			require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)
			require.NoError(t, store.failureContextErrors[0])
		})
	}
}

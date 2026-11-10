package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// 1. Frequent rejections without reservations:
// Verifies that when concurrency or rate limits are exceeded, requests fail immediately
// without creating any reservations in the database and without invoking provider dispatch.
func TestAIPaidCall_FrequentRejectionsDoNotCreateReservations(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 1, // strictly 1 slot
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             10 * time.Second,
		HeartbeatInterval:    2 * time.Second,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	accountingStore := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}
	svc := NewAIPaidCallerService(accountingStore, WithAILimiter(limiter))

	orgID := uuid.New()
	userID := int64(100)
	ctx := domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{
		OrganizationID: orgID,
		UserID:         &userID,
	})

	dispatchStarted := make(chan struct{})
	releaseDispatch := make(chan struct{})

	// Launch Call 1 in background which holds the slot
	go func() {
		params := validTestParams()
		params.IdempotencyKey = "op-first-call"
		_, _ = svc.ExecutePaidCall(ctx, params, func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
			close(dispatchStarted)
			<-releaseDispatch
			return "done", domain.AISettleRequest{
				Usage: domain.AICreditUsage{
					Status:       "verified",
					InputTokens:  int64Ptr(100),
					OutputTokens: int64Ptr(200),
				},
			}, nil
		})
	}()

	<-dispatchStarted

	// Now send 10 rapid concurrent attempts for the same user while Call 1 is in progress.
	// ALL 10 MUST fail with ErrAIConcurrencyLimitExceeded.
	// NONE of them must create a reservation in the database or invoke dispatch!
	dispatchCalls := 0
	initialReserveCalls := accountingStore.reserveCalls

	for i := 0; i < 10; i++ {
		params := validTestParams()
		params.IdempotencyKey = uuid.NewString()

		_, err := svc.ExecutePaidCall(ctx, params, func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
			dispatchCalls++
			return "should-not-run", domain.AISettleRequest{}, nil
		})

		require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)
	}

	require.Zero(t, dispatchCalls, "dispatch must not be called when limit rejected")
	require.Equal(t, initialReserveCalls, accountingStore.reserveCalls, "database reservations must not be created for rejected calls")

	// Complete Call 1
	close(releaseDispatch)
}

// 2. Client cancellation does not prematurely release the slot:
// Verifies that a dropped HTTP client connection does NOT free the slot while
// generation / accounting completion is still ongoing in the background.
func TestAIPaidCall_ClientCancellationDoesNotPrematurelyReleaseSlot(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 1, // only 1 slot
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             10 * time.Second,
		HeartbeatInterval:    2 * time.Second,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	accountingStore := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}
	svc := NewAIPaidCallerService(accountingStore, WithAILimiter(limiter))

	orgID := uuid.New()
	userID := int64(200)

	clientCtx, clientCancel := context.WithCancel(context.Background())
	reqCtx := domain.WithAIPaidCaller(clientCtx, domain.AIPaidCaller{
		OrganizationID: orgID,
		UserID:         &userID,
	})

	inDispatch := make(chan struct{})
	finishDispatch := make(chan struct{})
	callFinished := make(chan struct{})

	go func() {
		defer close(callFinished)
		params := validTestParams()
		params.IdempotencyKey = "op-cancel-test"

		_, _ = svc.ExecutePaidCall(reqCtx, params, func(ctx context.Context, reservation domain.AIReservation) (any, domain.AISettleRequest, error) {
			close(inDispatch)
			// Wait until test signals that HTTP client disconnected
			<-finishDispatch
			return "resp", domain.AISettleRequest{
				Usage: domain.AICreditUsage{
					Status:       "verified",
					InputTokens:  int64Ptr(50),
					OutputTokens: int64Ptr(50),
				},
			}, nil
		})
	}()

	<-inDispatch

	// Simulate client disconnecting (HTTP connection broken / window closed)
	clientCancel()

	// While work is still finishing, another request MUST still be rejected!
	// Client cancellation must NOT have immediately released the slot!
	anotherCtx := domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{
		OrganizationID: orgID,
		UserID:         &userID,
	})
	anotherParams := validTestParams()
	anotherParams.IdempotencyKey = "op-another-req"

	_, err := svc.ExecutePaidCall(anotherCtx, anotherParams, func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
		return "should-fail", domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded, "slot must stay held while work and settlement finish")

	// Allow the background work to finish
	close(finishDispatch)
	<-callFinished

	// After full completion, a new request can now acquire the slot
	recoveredResp, _, _, err := ExecutePaidCallTyped(anotherCtx, svc, anotherParams, func(ctx context.Context, res domain.AIReservation) (string, domain.AISettleRequest, error) {
		return "recovered", domain.AISettleRequest{
			Usage: domain.AICreditUsage{
				Status:       "verified",
				InputTokens:  int64Ptr(10),
				OutputTokens: int64Ptr(20),
			},
		}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "recovered", recoveredResp)
}

// 3. Lease expiration does NOT drop provider expense:
// If a lease times out due to long execution, but the provider successfully returns
// generation evidence, accounting settlement MUST still commit the real expense.
func TestAIPaidCall_LeaseExpirationDoesNotDropProviderExpense(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 1,
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             50 * time.Millisecond,  // very short TTL
		HeartbeatInterval:    200 * time.Millisecond, // heartbeat intentionally slower than TTL to let lease expire
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	accountingStore := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}
	svc := NewAIPaidCallerService(accountingStore, WithAILimiter(limiter))

	orgID := uuid.New()
	userID := int64(300)
	ctx := domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{
		OrganizationID: orgID,
		UserID:         &userID,
	})

	params := validTestParams()
	params.IdempotencyKey = "op-expire-expense-test"

	resp, reservation, settlement, err := ExecutePaidCallTyped(ctx, svc, params, func(ctx context.Context, res domain.AIReservation) (string, domain.AISettleRequest, error) {
		// Provider takes longer than LeaseTTL
		time.Sleep(80 * time.Millisecond)

		// Provider successfully generated tokens
		return "provider text response", domain.AISettleRequest{
			Usage: domain.AICreditUsage{
				Status:       "verified",
				InputTokens:  int64Ptr(500),
				OutputTokens: int64Ptr(1500),
			},
		}, nil
	})

	require.NoError(t, err)
	require.Equal(t, "provider text response", resp)
	require.Len(t, accountingStore.completed, 1, "settlement must still commit even if the lease expired")
	require.NotNil(t, settlement)
	require.Equal(t, "settled", reservation.AttemptStatus)
}

// 4. Two API instances coordination through shared limiter:
// Verifies that instances A and B strictly respect total concurrent slots per user.
func TestAIPaidCall_TwoInstances_MultiNodeCoordination(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 2, // 2 parallel generations allowed for the user
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             10 * time.Second,
		HeartbeatInterval:    2 * time.Second,
		FailClosed:           true,
	}

	limiterNodeA := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	limiterNodeB := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)

	accountingStore := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}

	svcA := NewAIPaidCallerService(accountingStore, WithAILimiter(limiterNodeA))
	svcB := NewAIPaidCallerService(accountingStore, WithAILimiter(limiterNodeB))

	orgID := uuid.New()
	userID := int64(400)
	ctx := domain.WithAIPaidCaller(context.Background(), domain.AIPaidCaller{
		OrganizationID: orgID,
		UserID:         &userID,
	})

	startA := make(chan struct{})
	startB := make(chan struct{})
	releaseAll := make(chan struct{})

	var wg sync.WaitGroup

	// Slot 1 running on Node A
	wg.Add(1)
	go func() {
		defer wg.Done()
		p := validTestParams()
		p.IdempotencyKey = "op-node-a"
		_, _ = svcA.ExecutePaidCall(ctx, p, func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
			close(startA)
			<-releaseAll
			return "respA", domain.AISettleRequest{Usage: domain.AICreditUsage{Status: "verified", InputTokens: int64Ptr(10), OutputTokens: int64Ptr(10)}}, nil
		})
	}()

	// Slot 2 running on Node B
	wg.Add(1)
	go func() {
		defer wg.Done()
		p := validTestParams()
		p.IdempotencyKey = "op-node-b"
		_, _ = svcB.ExecutePaidCall(ctx, p, func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
			close(startB)
			<-releaseAll
			return "respB", domain.AISettleRequest{Usage: domain.AICreditUsage{Status: "verified", InputTokens: int64Ptr(10), OutputTokens: int64Ptr(10)}}, nil
		})
	}()

	<-startA
	<-startB

	// Now Node A attempts a 3rd call for the same user -> Rejected
	p3 := validTestParams()
	p3.IdempotencyKey = "op-node-a-third"
	_, errA := svcA.ExecutePaidCall(ctx, p3, func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
		return "fail", domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, errA, domain.ErrAIConcurrencyLimitExceeded)

	// Node B also attempts a 3rd call for the same user -> Rejected
	p4 := validTestParams()
	p4.IdempotencyKey = "op-node-b-third"
	_, errB := svcB.ExecutePaidCall(ctx, p4, func(ctx context.Context, res domain.AIReservation) (any, domain.AISettleRequest, error) {
		return "fail", domain.AISettleRequest{}, nil
	})
	require.ErrorIs(t, errB, domain.ErrAIConcurrencyLimitExceeded)

	// Release both active calls
	close(releaseAll)
	wg.Wait()

	// Once slots are freed, next request on Node A succeeds
	respA2, _, _, err := ExecutePaidCallTyped(ctx, svcA, p3, func(ctx context.Context, res domain.AIReservation) (string, domain.AISettleRequest, error) {
		return "ok-node-a", domain.AISettleRequest{Usage: domain.AICreditUsage{Status: "verified", InputTokens: int64Ptr(5), OutputTokens: int64Ptr(5)}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok-node-a", respA2)
}

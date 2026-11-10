package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// 1. Two instances test with shared store: verifies concurrency limit across API instances.
func TestAILimiter_TwoInstances_ConcurrencyEnforcement(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 2,
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             10 * time.Second,
		HeartbeatInterval:    2 * time.Second,
		FailClosed:           true,
	}

	instance1 := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	instance2 := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)

	ctx := context.Background()
	userID := int64(1001)

	// Instance 1 acquires slot 1
	lease1, err := instance1.Acquire(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, lease1)
	require.Equal(t, userID, lease1.UserID())

	// Instance 2 acquires slot 2 for the same user
	lease2, err := instance2.Acquire(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, lease2)

	// Third attempt on instance 1 must fail with concurrency limit
	_, err = instance1.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)

	// Third attempt on instance 2 must also fail with concurrency limit
	_, err = instance2.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)

	// Another user is independent and can acquire slots
	otherUserID := int64(1002)
	otherLease, err := instance1.Acquire(ctx, otherUserID)
	require.NoError(t, err)
	require.NotNil(t, otherLease)

	// Release slot 1 from instance 1
	require.NoError(t, lease1.Release(ctx))

	// Now instance 2 can acquire the freed slot for user 1001
	lease3, err := instance2.Acquire(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, lease3)

	// Clean up
	_ = lease2.Release(ctx)
	_ = lease3.Release(ctx)
	_ = otherLease.Release(ctx)
}

// 2. Sliding window rate limit test
func TestAILimiter_RateLimit_SlidingWindow(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 10,
		RateLimitRequests:    3,
		RateLimitWindow:      500 * time.Millisecond,
		LeaseTTL:             10 * time.Second,
		HeartbeatInterval:    2 * time.Second,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	ctx := context.Background()
	userID := int64(2001)

	var leases []AILease
	for i := 0; i < 3; i++ {
		l, err := limiter.Acquire(ctx, userID)
		require.NoError(t, err)
		leases = append(leases, l)
	}

	// 4th request within the window exceeds the rate limit
	_, err := limiter.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAIRateLimitExceeded)

	// Wait for the sliding window to roll over
	time.Sleep(600 * time.Millisecond)

	// After window expiration, requests succeed again
	l4, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	leases = append(leases, l4)

	for _, l := range leases {
		_ = l.Release(ctx)
	}
}

// 3. Process crash recovery test: expired lease is pruned automatically after LeaseTTL.
func TestAILimiter_CrashRecovery_ExpiredLeasePruned(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 1, // only 1 slot
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             80 * time.Millisecond, // very short TTL for test
		HeartbeatInterval:    20 * time.Millisecond,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	ctx := context.Background()
	userID := int64(3001)

	// Node A acquires slot and "crashes" (never calls Renew and never calls Release)
	crashedLease, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, crashedLease)

	// Immediately, next request is rejected because slot is still active
	_, err = limiter.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)

	// Wait until LeaseTTL expires (crashed lease times out)
	time.Sleep(120 * time.Millisecond)

	// New request on Node B automatically prunes the expired lease and acquires the slot
	recoveredLease, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, recoveredLease)
	require.NotEqual(t, crashedLease.ID(), recoveredLease.ID())

	_ = recoveredLease.Release(ctx)
}

// 4. Safe Release test: late release from an expired lease MUST NOT remove another owner's slot.
func TestAILimiter_SafeRelease_LateReleaseDoesNotTouchOtherOwnerSlot(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 1, // only 1 slot
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             80 * time.Millisecond,
		HeartbeatInterval:    20 * time.Millisecond,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	ctx := context.Background()
	userID := int64(4001)

	// Owner 1 acquires lease1
	lease1, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)

	// Wait for lease1 to expire
	time.Sleep(120 * time.Millisecond)

	// Owner 2 acquires the now-free slot
	lease2, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, lease2)

	// Owner 1 wakes up late and attempts to Release lease1
	err = lease1.Release(ctx)
	require.NoError(t, err)

	// Crucial check: Owner 2's slot MUST STILL BE INTACT!
	// A 3rd acquire attempt MUST fail because Owner 2 is still holding the slot!
	_, err = limiter.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded, "late release of lease1 must not have deleted lease2")

	// Owner 2 releases their slot
	require.NoError(t, lease2.Release(ctx))

	// Now a new acquire can succeed
	lease3, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	_ = lease3.Release(ctx)
}

// 5. Fail-closed behavior when storage is unavailable.
func TestAILimiter_FailClosed_WhenStoreUnavailable(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 2,
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             10 * time.Second,
		HeartbeatInterval:    2 * time.Second,
		FailClosed:           true, // fail-closed enabled
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	ctx := context.Background()
	userID := int64(5001)

	// Normal acquire works
	lease, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	_ = lease.Release(ctx)

	// Simulate storage / network crash
	sharedStore.SetSimulatedError(errors.New("redis connection refused"))

	// Subsequent acquire must fail-closed with ErrAILimiterUnavailable
	_, err = limiter.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAILimiterUnavailable)

	// Check with nil store and fail-closed
	nilLimiter := NewAIGenerationLimiterWithStore(nil, zap.NewNop(), cfg)
	_, err = nilLimiter.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAILimiterUnavailable)
}

// 6. Heartbeat lease renewal extends TTL.
func TestAILimiter_RenewHeartbeat(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 1,
		RateLimitRequests:    100,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             100 * time.Millisecond,
		HeartbeatInterval:    30 * time.Millisecond,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	ctx := context.Background()
	userID := int64(6001)

	lease, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)

	// Sleep 60ms, then renew
	time.Sleep(60 * time.Millisecond)
	require.NoError(t, lease.Renew(ctx))

	// Sleep another 60ms (total 120ms > initial LeaseTTL of 100ms)
	time.Sleep(60 * time.Millisecond)

	// Because of Renew, the lease is still active! Another acquire must be rejected
	_, err = limiter.Acquire(ctx, userID)
	require.ErrorIs(t, err, domain.ErrAIConcurrencyLimitExceeded)

	// Clean up
	require.NoError(t, lease.Release(ctx))

	// After release, new acquire succeeds
	lease2, err := limiter.Acquire(ctx, userID)
	require.NoError(t, err)
	_ = lease2.Release(ctx)
}

// 7. Concurrent race condition test with multiple goroutines.
func TestAILimiter_ConcurrentRace(t *testing.T) {
	sharedStore := NewMemorySharedLimiterStore()
	cfg := config.AILimiterConfig{
		Enabled:              true,
		MaxConcurrentPerUser: 2,
		RateLimitRequests:    20,
		RateLimitWindow:      time.Minute,
		LeaseTTL:             5 * time.Second,
		HeartbeatInterval:    1 * time.Second,
		FailClosed:           true,
	}

	limiter := NewAIGenerationLimiterWithStore(sharedStore, zap.NewNop(), cfg)
	userID := int64(7001)

	var wg sync.WaitGroup
	var acquiredMu sync.Mutex
	acquiredCount := 0
	concurrencyLimitExceededCount := 0
	var acquiredLeases []AILease

	// Launch 10 concurrent acquire requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := limiter.Acquire(context.Background(), userID)
			acquiredMu.Lock()
			defer acquiredMu.Unlock()
			if err == nil {
				acquiredCount++
				acquiredLeases = append(acquiredLeases, l)
			} else if errors.Is(err, domain.ErrAIConcurrencyLimitExceeded) {
				concurrencyLimitExceededCount++
			}
		}()
	}

	wg.Wait()
	require.Equal(t, 2, acquiredCount, "strictly max 2 concurrent slots must be acquired")
	require.Equal(t, 8, concurrencyLimitExceededCount, "other 8 requests must be rejected by concurrency limit")

	for _, l := range acquiredLeases {
		_ = l.Release(context.Background())
	}
}

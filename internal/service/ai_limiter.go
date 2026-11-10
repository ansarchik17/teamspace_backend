package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// AILease represents an acquired concurrency slot owned by an authenticated user.
type AILease interface {
	// ID returns the unique token identifying this lease owner (UUID).
	ID() string
	// UserID returns the authenticated user owning this lease.
	UserID() int64
	// Renew extends the active lease TTL in the shared store.
	Renew(ctx context.Context) error
	// Release safely frees the concurrency slot in the shared store, strictly guaranteeing
	// only the owner (matching ID) can release it, without touching others' slots.
	Release(ctx context.Context) error
}

// AIGenerationLimiter regulates generation frequency and concurrency per user
// across multiple API instances using a shared Redis store.
type AIGenerationLimiter interface {
	// Acquire checks rate limits and attempts to acquire a concurrency slot
	// for the given authenticated user before reservation or provider dispatch.
	Acquire(ctx context.Context, userID int64) (AILease, error)
	// Config returns the active limiter configuration.
	Config() config.AILimiterConfig
	// HeartbeatInterval returns the configured heartbeat interval.
	HeartbeatInterval() time.Duration
}

// aiLimiterStore abstracts the underlying shared storage operations.
type aiLimiterStore interface {
	Acquire(ctx context.Context, userID int64, leaseID string, now time.Time, cfg config.AILimiterConfig) (string, error)
	Renew(ctx context.Context, userID int64, leaseID string, now time.Time, cfg config.AILimiterConfig) (bool, error)
	Release(ctx context.Context, userID int64, leaseID string) error
}

type defaultAIGenerationLimiter struct {
	store  aiLimiterStore
	logger *zap.Logger
	cfg    config.AILimiterConfig
}

// NewAIGenerationLimiter creates a new limiter backed by Redis.
func NewAIGenerationLimiter(redisClient *redis.Client, logger *zap.Logger, cfg config.AILimiterConfig) AIGenerationLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}
	var store aiLimiterStore
	if redisClient != nil {
		store = newRedisLimiterStore(redisClient)
	}
	return &defaultAIGenerationLimiter{
		store:  store,
		logger: logger,
		cfg:    cfg,
	}
}

// NewAIGenerationLimiterWithStore creates a limiter with a custom shared store (for testing or alternate backends).
func NewAIGenerationLimiterWithStore(store aiLimiterStore, logger *zap.Logger, cfg config.AILimiterConfig) AIGenerationLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &defaultAIGenerationLimiter{
		store:  store,
		logger: logger,
		cfg:    cfg,
	}
}

func (l *defaultAIGenerationLimiter) Config() config.AILimiterConfig {
	return l.cfg
}

func (l *defaultAIGenerationLimiter) HeartbeatInterval() time.Duration {
	if l.cfg.HeartbeatInterval <= 0 {
		return 15 * time.Second
	}
	return l.cfg.HeartbeatInterval
}

func (l *defaultAIGenerationLimiter) Acquire(ctx context.Context, userID int64) (AILease, error) {
	if userID <= 0 {
		return nil, domain.ErrAIPaidCallInvalidCaller
	}
	if !l.cfg.Enabled {
		return &noopLease{id: uuid.NewString(), userID: userID}, nil
	}
	if l.store == nil {
		if l.cfg.FailClosed {
			l.logger.Error("AI generation limiter store is unavailable (fail-closed)", zap.Int64("user_id", userID))
			return nil, domain.ErrAILimiterUnavailable
		}
		l.logger.Warn("AI generation limiter store is unavailable (fail-open)", zap.Int64("user_id", userID))
		return &noopLease{id: uuid.NewString(), userID: userID}, nil
	}

	leaseID := uuid.NewString()
	status, err := l.store.Acquire(ctx, userID, leaseID, time.Now(), l.cfg)
	if err != nil {
		l.logger.Error("AI generation limiter store error during acquire", zap.Int64("user_id", userID), zap.Error(err))
		if l.cfg.FailClosed {
			return nil, fmt.Errorf("%w: %v", domain.ErrAILimiterUnavailable, err)
		}
		return &noopLease{id: leaseID, userID: userID}, nil
	}

	switch status {
	case "rate_limit_exceeded":
		return nil, domain.ErrAIRateLimitExceeded
	case "concurrency_limit_exceeded":
		return nil, domain.ErrAIConcurrencyLimitExceeded
	case "ok":
		return &aiLeaseImpl{
			store:   l.store,
			userID:  userID,
			leaseID: leaseID,
			cfg:     l.cfg,
			logger:  l.logger,
		}, nil
	default:
		l.logger.Error("Unexpected acquire status from limiter store", zap.String("status", status))
		return nil, domain.ErrAILimiterUnavailable
	}
}

// aiLeaseImpl represents an active lease in the store.
type aiLeaseImpl struct {
	store     aiLimiterStore
	userID    int64
	leaseID   string
	cfg       config.AILimiterConfig
	logger    *zap.Logger
	releaseMu sync.Mutex
	released  bool
}

func (l *aiLeaseImpl) ID() string {
	return l.leaseID
}

func (l *aiLeaseImpl) UserID() int64 {
	return l.userID
}

func (l *aiLeaseImpl) Renew(ctx context.Context) error {
	l.releaseMu.Lock()
	if l.released {
		l.releaseMu.Unlock()
		return errors.New("lease is already released")
	}
	l.releaseMu.Unlock()

	ok, err := l.store.Renew(ctx, l.userID, l.leaseID, time.Now(), l.cfg)
	if err != nil {
		l.logger.Warn("Failed to renew AI generation lease", zap.String("lease_id", l.leaseID), zap.Error(err))
		return err
	}
	if !ok {
		l.logger.Warn("AI generation lease expired or not found during renewal", zap.String("lease_id", l.leaseID))
		return errors.New("lease expired")
	}
	return nil
}

func (l *aiLeaseImpl) Release(ctx context.Context) error {
	l.releaseMu.Lock()
	if l.released {
		l.releaseMu.Unlock()
		return nil
	}
	l.released = true
	l.releaseMu.Unlock()

	return l.store.Release(ctx, l.userID, l.leaseID)
}

// noopLease is used when limiting is disabled or fail-open without storage.
type noopLease struct {
	id     string
	userID int64
}

func (l *noopLease) ID() string                    { return l.id }
func (l *noopLease) UserID() int64                 { return l.userID }
func (l *noopLease) Renew(context.Context) error   { return nil }
func (l *noopLease) Release(context.Context) error { return nil }

// =========================================================================
// Redis Implementation using atomic Lua scripts
// =========================================================================

type redisLimiterStore struct {
	client redis.Cmdable
}

func newRedisLimiterStore(client redis.Cmdable) aiLimiterStore {
	return &redisLimiterStore{client: client}
}

// Lua script to atomically check rate limit, concurrency, and acquire slot.
var luaAcquire = redis.NewScript(`
local rateKey = KEYS[1]
local concurKey = KEYS[2]
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000000000 + tonumber(clock[2]) * 1000
local rateWindow = tonumber(ARGV[2])
local maxReqs = tonumber(ARGV[3])
local leaseID = ARGV[4]
local leaseTTLNano = tonumber(ARGV[5])
local leaseTTLSec = tonumber(ARGV[6])
local maxConcur = tonumber(ARGV[7])

-- 1. Check Rate Limit (sliding window via sorted set)
local windowStart = now - rateWindow
redis.call('ZREMRANGEBYSCORE', rateKey, '-inf', tostring(windowStart))
local currentReqs = redis.call('ZCARD', rateKey)
if currentReqs >= maxReqs then
    return 'rate_limit_exceeded'
end

-- 2. Check Concurrency Limit
-- Prune expired leases (crashed/dead instances recovery)
redis.call('ZREMRANGEBYSCORE', concurKey, '-inf', tostring(now))
local activeSlots = redis.call('ZCARD', concurKey)
if activeSlots >= maxConcur then
    return 'concurrency_limit_exceeded'
end

-- 3. Record rate limit event
redis.call('ZADD', rateKey, tostring(now), tostring(now) .. ':' .. leaseID)
local rateExpireSec = math.ceil(rateWindow / 1000000000) + 1
redis.call('EXPIRE', rateKey, rateExpireSec)

-- 4. Record active concurrency lease
local expireAt = now + leaseTTLNano
redis.call('ZADD', concurKey, tostring(expireAt), leaseID)
local concurExpireSec = leaseTTLSec + 120
redis.call('EXPIRE', concurKey, concurExpireSec)

return 'ok'
`)

// Lua script to renew active lease.
var luaRenew = redis.NewScript(`
local concurKey = KEYS[1]
local leaseID = ARGV[1]
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000000000 + tonumber(clock[2]) * 1000
local leaseTTLNano = tonumber(ARGV[3])
local leaseTTLSec = tonumber(ARGV[4])

local score = redis.call('ZSCORE', concurKey, leaseID)
if not score or tonumber(score) <= now then
    return 0
end

local newExpireAt = now + leaseTTLNano
redis.call('ZADD', concurKey, tostring(newExpireAt), leaseID)
local concurExpireSec = leaseTTLSec + 120
redis.call('EXPIRE', concurKey, concurExpireSec)
return 1
`)

// Lua script to safely release lease owned by this leaseID.
var luaRelease = redis.NewScript(`
local concurKey = KEYS[1]
local leaseID = ARGV[1]

-- Only remove this specific leaseID belonging to the owner
return redis.call('ZREM', concurKey, leaseID)
`)

func (r *redisLimiterStore) Acquire(ctx context.Context, userID int64, leaseID string, now time.Time, cfg config.AILimiterConfig) (string, error) {
	rateKey := fmt.Sprintf("ai:ratelimit:user:%d", userID)
	concurKey := fmt.Sprintf("ai:concurrency:user:%d", userID)

	nowNano := now.UnixNano()
	rateWindowNano := cfg.RateLimitWindow.Nanoseconds()
	leaseTTLNano := cfg.LeaseTTL.Nanoseconds()
	leaseTTLSec := int(math.Ceil(cfg.LeaseTTL.Seconds()))

	keys := []string{rateKey, concurKey}
	args := []any{
		nowNano,
		rateWindowNano,
		cfg.RateLimitRequests,
		leaseID,
		leaseTTLNano,
		leaseTTLSec,
		cfg.MaxConcurrentPerUser,
	}

	res, err := luaAcquire.Run(ctx, r.client, keys, args...).Text()
	if err != nil {
		return "", err
	}
	return res, nil
}

func (r *redisLimiterStore) Renew(ctx context.Context, userID int64, leaseID string, now time.Time, cfg config.AILimiterConfig) (bool, error) {
	concurKey := fmt.Sprintf("ai:concurrency:user:%d", userID)

	nowNano := now.UnixNano()
	leaseTTLNano := cfg.LeaseTTL.Nanoseconds()
	leaseTTLSec := int(math.Ceil(cfg.LeaseTTL.Seconds()))

	keys := []string{concurKey}
	args := []any{
		leaseID,
		nowNano,
		leaseTTLNano,
		leaseTTLSec,
	}

	res, err := luaRenew.Run(ctx, r.client, keys, args...).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func (r *redisLimiterStore) Release(ctx context.Context, userID int64, leaseID string) error {
	concurKey := fmt.Sprintf("ai:concurrency:user:%d", userID)
	keys := []string{concurKey}
	args := []any{leaseID}

	_, err := luaRelease.Run(ctx, r.client, keys, args...).Result()
	return err
}

// =========================================================================
// In-Memory Shared Store for Testing Multi-Instance Coordination & Faults
// =========================================================================

// MemorySharedLimiterStore simulates a shared Redis instance accessible by multiple API nodes.
type MemorySharedLimiterStore struct {
	mu           sync.Mutex
	rateEvents   map[int64][]int64          // userID -> slice of timestamps (UnixNano)
	activeLeases map[int64]map[string]int64 // userID -> leaseID -> expireAtUnixNano
	simulatedErr error
}

// NewMemorySharedLimiterStore creates a thread-safe shared store for multi-instance tests.
func NewMemorySharedLimiterStore() *MemorySharedLimiterStore {
	return &MemorySharedLimiterStore{
		rateEvents:   make(map[int64][]int64),
		activeLeases: make(map[int64]map[string]int64),
	}
}

// SetSimulatedError injects a network/storage failure to test fail-closed behavior.
func (m *MemorySharedLimiterStore) SetSimulatedError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.simulatedErr = err
}

func (m *MemorySharedLimiterStore) Acquire(ctx context.Context, userID int64, leaseID string, now time.Time, cfg config.AILimiterConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.simulatedErr != nil {
		return "", m.simulatedErr
	}

	nowNano := now.UnixNano()
	windowStart := nowNano - cfg.RateLimitWindow.Nanoseconds()

	// 1. Sliding window rate limit check
	events := m.rateEvents[userID]
	validEvents := make([]int64, 0, len(events))
	for _, ts := range events {
		if ts > windowStart {
			validEvents = append(validEvents, ts)
		}
	}
	m.rateEvents[userID] = validEvents

	if len(validEvents) >= cfg.RateLimitRequests {
		return "rate_limit_exceeded", nil
	}

	// 2. Concurrency limit check & expired leases pruning
	userLeases, exists := m.activeLeases[userID]
	if !exists {
		userLeases = make(map[string]int64)
		m.activeLeases[userID] = userLeases
	}
	for id, expireAt := range userLeases {
		if expireAt <= nowNano {
			delete(userLeases, id)
		}
	}

	if len(userLeases) >= cfg.MaxConcurrentPerUser {
		return "concurrency_limit_exceeded", nil
	}

	// 3. Register request event & acquire lease
	m.rateEvents[userID] = append(m.rateEvents[userID], nowNano)
	userLeases[leaseID] = nowNano + cfg.LeaseTTL.Nanoseconds()

	return "ok", nil
}

func (m *MemorySharedLimiterStore) Renew(ctx context.Context, userID int64, leaseID string, now time.Time, cfg config.AILimiterConfig) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.simulatedErr != nil {
		return false, m.simulatedErr
	}

	userLeases, exists := m.activeLeases[userID]
	if !exists {
		return false, nil
	}

	expireAt, found := userLeases[leaseID]
	if !found {
		return false, nil
	}

	nowNano := now.UnixNano()
	if expireAt <= nowNano {
		delete(userLeases, leaseID)
		return false, nil
	}

	userLeases[leaseID] = nowNano + cfg.LeaseTTL.Nanoseconds()
	return true, nil
}

func (m *MemorySharedLimiterStore) Release(ctx context.Context, userID int64, leaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.simulatedErr != nil {
		return m.simulatedErr
	}

	userLeases, exists := m.activeLeases[userID]
	if !exists {
		return nil
	}

	// Safe Release: strictly delete only this leaseID
	delete(userLeases, leaseID)
	return nil
}

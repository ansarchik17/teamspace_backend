package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrEgovStateNotFound = errors.New("egov state not found")
	ErrEgovStateExpired  = errors.New("egov state expired")
	ErrEgovStateReused   = errors.New("egov state reused")
)

type EgovAuthState struct {
	State          string
	RedirectTarget string
	Scopes         []string
	Lang           string
	ExpiresAt      time.Time
}

type EgovStateStore interface {
	Save(ctx context.Context, state *EgovAuthState, ttl time.Duration) error
	Consume(ctx context.Context, state string) (*EgovAuthState, error)
}

type InMemoryEgovStateStore struct {
	mu       sync.Mutex
	items    map[string]*EgovAuthState
	consumed map[string]*egovConsumedState
}

type egovConsumedState struct {
	consumedAt time.Time
	expiresAt  time.Time
}

func NewInMemoryEgovStateStore() *InMemoryEgovStateStore {
	return &InMemoryEgovStateStore{
		items:    make(map[string]*EgovAuthState),
		consumed: make(map[string]*egovConsumedState),
	}
}

func (s *InMemoryEgovStateStore) Save(_ context.Context, state *EgovAuthState, ttl time.Duration) error {
	if state == nil || state.State == "" {
		return fmt.Errorf("egov state is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("egov state ttl must be positive")
	}
	copyValue := *state
	copyValue.ExpiresAt = time.Now().Add(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	s.items[state.State] = &copyValue
	delete(s.consumed, state.State)
	return nil
}

func (s *InMemoryEgovStateStore) Consume(_ context.Context, state string) (*EgovAuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.cleanupLocked(now)
	item, ok := s.items[state]
	if !ok {
		if consumed, exists := s.consumed[state]; exists {
			if now.Before(consumed.expiresAt) || now.Equal(consumed.expiresAt) {
				return nil, ErrEgovStateReused
			}
			delete(s.consumed, state)
			return nil, ErrEgovStateExpired
		}
		return nil, ErrEgovStateNotFound
	}
	delete(s.items, state)
	if now.After(item.ExpiresAt) {
		s.consumed[state] = &egovConsumedState{
			consumedAt: now,
			expiresAt:  item.ExpiresAt,
		}
		return nil, ErrEgovStateExpired
	}
	copyValue := *item
	s.consumed[state] = &egovConsumedState{
		consumedAt: now,
		expiresAt:  item.ExpiresAt,
	}
	return &copyValue, nil
}

func (s *InMemoryEgovStateStore) cleanupLocked(now time.Time) {
	for key, value := range s.items {
		if value == nil || now.After(value.ExpiresAt) {
			delete(s.items, key)
		}
	}
	for key, value := range s.consumed {
		if value == nil || now.After(value.expiresAt) {
			delete(s.consumed, key)
		}
	}
}

package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"os"
	"sync"
	"time"
)

type growySessionError struct {
	status int
	code   string
}

func (e *growySessionError) Error() string { return e.code }

type growySessionEntry struct {
	access  string
	expires time.Time
	ready   chan struct{}
}

// Per-handler, process-local credentials only. Concurrent page requests share
// an exchange; cancellations and failures are never cached. No access decisions
// or business data are cached.
type growySessionCache struct {
	mu      sync.Mutex
	entries map[string]*growySessionEntry
}

func growySessionExpiry(access string, seconds int, started time.Time) time.Time {
	// Parsing is only for expiry of a token received directly from the trusted
	// upstream over TLS, never for authentication of client-supplied credentials.
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(access, claims); err != nil {
		return time.Time{}
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil || seconds <= 10 {
		return time.Time{}
	}
	if seconds > 70 {
		seconds = 70
	}
	until := started.Add(time.Duration(seconds-10) * time.Second)
	if tokenUntil := exp.Time.Add(-10 * time.Second); tokenUntil.Before(until) {
		until = tokenUntil
	}
	return until
}

func (s *growySessionCache) get(ctx context.Context, key string, load func() (string, time.Time, error)) (string, bool, error) {
	for {
		s.mu.Lock()
		if s.entries == nil {
			s.entries = make(map[string]*growySessionEntry)
		}
		if entry := s.entries[key]; entry != nil {
			if entry.ready != nil {
				ready := entry.ready
				s.mu.Unlock()
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return "", false, ctx.Err()
				}
			}
			if time.Now().Before(entry.expires) {
				s.mu.Unlock()
				return entry.access, true, nil
			}
			delete(s.entries, key)
		}
		// Bound retained credentials. In-flight entries are left intact.
		if len(s.entries) >= 1024 {
			for k, e := range s.entries {
				if e.ready == nil {
					delete(s.entries, k)
				}
			}
		}
		entry := &growySessionEntry{ready: make(chan struct{})}
		s.entries[key] = entry
		s.mu.Unlock()
		access, expires, err := load()
		s.mu.Lock()
		ready := entry.ready
		entry.ready = nil
		if err == nil && ctx.Err() == nil && time.Now().Before(expires) {
			entry.access, entry.expires = access, expires
		} else {
			delete(s.entries, key)
		}
		close(ready)
		s.mu.Unlock()
		return access, false, err
	}
}

func (s *growySessionCache) invalidate(key, access string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[key]; e != nil && e.ready == nil && e.access == access {
		delete(s.entries, key)
	}
}

func growySessionKey(base string, claims *auth.Claims, details *domain.GrowyHandoffDetails) string {
	identity, _ := json.Marshal([]any{base, claims.UserID, claims.TokenID, claims.Email, claims.Username, claims.FirstName, claims.LastName, details,
		os.Getenv("GROWY_SSO_ISSUER"), os.Getenv("GROWY_SSO_PRIVATE_KEY_DER_B64"), os.Getenv("GROWY_TENANT_PROVISION_URL"),
		os.Getenv("GROWY_API_INTERNAL_URL"), os.Getenv("GROWY_LMS_INTERNAL_URL"), os.Getenv("TENANT_BASE_DOMAIN")})
	return fmt.Sprintf("%x", sha256.Sum256(identity))
}

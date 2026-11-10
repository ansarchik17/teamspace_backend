package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	odoHandoffKeyPrefix = "auth:odo-handoff:"
	ODOHandoffTTL       = 60 * time.Second
)

var (
	ErrODOHandoffInvalid = errors.New("ODO handoff code is invalid or expired")
	ErrODOHandoffToken   = errors.New("workspace token is required")
)

// ODOHandoffStore exchanges a short-lived, single-use opaque code for the
// original Workspace access token. The access token is never exposed in a URL.
type ODOHandoffStore interface {
	Create(ctx context.Context, workspaceToken string) (string, error)
	Redeem(ctx context.Context, code string) (string, error)
}

type redisODOHandoffStore struct {
	client *redis.Client
}

type memoryODOHandoff struct {
	token     string
	expiresAt time.Time
}

type memoryODOHandoffStore struct {
	mu      sync.Mutex
	entries map[string]memoryODOHandoff
}

var redeemODOHandoffScript = redis.NewScript(`
local value = redis.call("GET", KEYS[1])
if value then
  redis.call("DEL", KEYS[1])
end
return value
`)

// NewODOHandoffStore uses Redis when available so a code can be created and
// redeemed by different Workspace instances. The in-memory implementation is
// intentionally only a development fallback.
func NewODOHandoffStore(client *redis.Client) ODOHandoffStore {
	if client != nil {
		return &redisODOHandoffStore{client: client}
	}
	return &memoryODOHandoffStore{entries: make(map[string]memoryODOHandoff)}
}

func (s *redisODOHandoffStore) Create(ctx context.Context, workspaceToken string) (string, error) {
	code, key, err := newODOHandoffCode(workspaceToken)
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, key, strings.TrimSpace(workspaceToken), ODOHandoffTTL).Err(); err != nil {
		return "", err
	}
	return code, nil
}

func (s *redisODOHandoffStore) Redeem(ctx context.Context, code string) (string, error) {
	key, err := odoHandoffKey(code)
	if err != nil {
		return "", err
	}
	value, err := redeemODOHandoffScript.Run(ctx, s.client, []string{key}).Text()
	if errors.Is(err, redis.Nil) {
		return "", ErrODOHandoffInvalid
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", ErrODOHandoffInvalid
	}
	return value, nil
}

func (s *memoryODOHandoffStore) Create(_ context.Context, workspaceToken string) (string, error) {
	code, key, err := newODOHandoffCode(workspaceToken)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.entries[key] = memoryODOHandoff{token: strings.TrimSpace(workspaceToken), expiresAt: time.Now().Add(ODOHandoffTTL)}
	s.mu.Unlock()
	return code, nil
}

func (s *memoryODOHandoffStore) Redeem(_ context.Context, code string) (string, error) {
	key, err := odoHandoffKey(code)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	entry, ok := s.entries[key]
	delete(s.entries, key)
	s.mu.Unlock()
	if !ok || time.Now().After(entry.expiresAt) || strings.TrimSpace(entry.token) == "" {
		return "", ErrODOHandoffInvalid
	}
	return entry.token, nil
}

func newODOHandoffCode(workspaceToken string) (string, string, error) {
	if strings.TrimSpace(workspaceToken) == "" {
		return "", "", ErrODOHandoffToken
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	code := base64.RawURLEncoding.EncodeToString(random)
	key, _ := odoHandoffKey(code)
	return code, key, nil
}

func odoHandoffKey(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", ErrODOHandoffInvalid
	}
	digest := sha256.Sum256([]byte(code))
	return odoHandoffKeyPrefix + base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

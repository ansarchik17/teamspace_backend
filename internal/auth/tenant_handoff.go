package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const tenantHandoffTTL = 30 * time.Second

var ErrTenantHandoffInvalid = errors.New("tenant handoff code is invalid or expired")

type TenantHandoff struct {
	UserID       int64  `json:"user_id"`
	Slug         string `json:"slug"`
	RefreshToken string `json:"refresh_token"`
}

// TenantHandoffStore is deliberately Redis-only: multiple API replicas must
// redeem the same code exactly once, and a missing Redis must fail closed.
type TenantHandoffStore struct {
	client *redis.Client
}

func NewTenantHandoffStore(client *redis.Client) *TenantHandoffStore {
	if client == nil {
		return nil
	}
	return &TenantHandoffStore{client: client}
}

func (s *TenantHandoffStore) Create(ctx context.Context, entry TenantHandoff) (string, error) {
	if s == nil || s.client == nil || entry.UserID <= 0 || entry.Slug == "" || entry.RefreshToken == "" {
		return "", ErrTenantHandoffInvalid
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(random)
	value, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, tenantHandoffKey(code), value, tenantHandoffTTL).Err(); err != nil {
		return "", err
	}
	return code, nil
}

func (s *TenantHandoffStore) Redeem(ctx context.Context, code string) (TenantHandoff, error) {
	var entry TenantHandoff
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(code)
	if s == nil || s.client == nil || decodeErr != nil || len(decoded) != 32 {
		return entry, ErrTenantHandoffInvalid
	}
	value, err := s.client.GetDel(ctx, tenantHandoffKey(code)).Bytes()
	if errors.Is(err, redis.Nil) {
		return entry, ErrTenantHandoffInvalid
	}
	if err != nil {
		return entry, err
	}
	if err := json.Unmarshal(value, &entry); err != nil || entry.UserID <= 0 || entry.Slug == "" || entry.RefreshToken == "" {
		return TenantHandoff{}, ErrTenantHandoffInvalid
	}
	return entry, nil
}

func tenantHandoffKey(code string) string {
	digest := sha256.Sum256([]byte(code))
	return "auth:tenant-handoff:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

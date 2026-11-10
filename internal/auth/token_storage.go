package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisTokenStorage implements TokenStorage using Redis
type RedisTokenStorage struct {
	client *redis.Client
}

type linkedTokenMetadata struct {
	UserID         int64  `json:"user_id"`
	LinkedDeviceID string `json:"linked_device_id,omitempty"`
	WebLinked      bool   `json:"web_linked"`
}

// NewRedisTokenStorage creates a new Redis token storage
func NewRedisTokenStorage(client *redis.Client) *RedisTokenStorage {
	return &RedisTokenStorage{
		client: client,
	}
}

// StoreToken stores a token in Redis with expiration
func (s *RedisTokenStorage) StoreToken(ctx context.Context, tokenID string, userID int64, expiresAt time.Time) error {
	key := fmt.Sprintf("token:%s", tokenID)
	userTokenKey := fmt.Sprintf("user:tokens:%d", userID)

	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return fmt.Errorf("token already expired")
	}

	// Use pipeline for atomic operations
	pipe := s.client.Pipeline()

	// Store token with its user ID
	pipe.Set(ctx, key, userID, ttl)

	// Add token to user's token set
	pipe.SAdd(ctx, userTokenKey, tokenID)
	pipe.Expire(ctx, userTokenKey, ttl)

	_, err := pipe.Exec(ctx)
	return err
}

// IsTokenValid checks if a token exists and is valid
func (s *RedisTokenStorage) IsTokenValid(ctx context.Context, tokenID string) (bool, error) {
	key := fmt.Sprintf("token:%s", tokenID)

	exists, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}

	return exists > 0, nil
}

// RevokeToken revokes a specific token
func (s *RedisTokenStorage) RevokeToken(ctx context.Context, tokenID string) error {
	key := fmt.Sprintf("token:%s", tokenID)

	// Get user ID before deleting
	userID, err := s.client.Get(ctx, key).Int64()
	if err != nil && err != redis.Nil {
		return err
	}

	// Delete token
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return err
	}

	// Remove from user's token set if user ID was found
	if userID > 0 {
		userTokenKey := fmt.Sprintf("user:tokens:%d", userID)
		s.client.SRem(ctx, userTokenKey, tokenID)
	}

	if err := s.cleanupLinkedTokenMetadata(ctx, tokenID); err != nil {
		return err
	}

	return nil
}

// RevokeAllUserTokens revokes all tokens for a user
func (s *RedisTokenStorage) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	userTokenKey := fmt.Sprintf("user:tokens:%d", userID)

	// Get all token IDs for the user
	tokenIDs, err := s.client.SMembers(ctx, userTokenKey).Result()
	if err != nil {
		return err
	}

	if len(tokenIDs) == 0 {
		return nil
	}

	// Delete all tokens
	pipe := s.client.Pipeline()
	for _, tokenID := range tokenIDs {
		key := fmt.Sprintf("token:%s", tokenID)
		pipe.Del(ctx, key)
		pipe.Del(ctx, linkedTokenMetadataKey(tokenID))
	}

	// Delete user's token set
	pipe.Del(ctx, userTokenKey)
	deviceIndexKey := linkedDeviceIndexKey(userID)
	deviceIDs, _ := s.client.SMembers(ctx, deviceIndexKey).Result()
	for _, deviceID := range deviceIDs {
		pipe.Del(ctx, linkedDeviceTokensKey(userID, deviceID))
	}
	pipe.Del(ctx, deviceIndexKey)

	_, err = pipe.Exec(ctx)
	return err
}

// Cleanup removes expired tokens (should be called periodically)
func (s *RedisTokenStorage) Cleanup(ctx context.Context) error {
	// Redis automatically removes expired keys, so this is a no-op
	// But we can add additional cleanup logic if needed
	return nil
}

// GetUserTokenCount returns the number of active tokens for a user
func (s *RedisTokenStorage) GetUserTokenCount(ctx context.Context, userID int64) (int64, error) {
	userTokenKey := fmt.Sprintf("user:tokens:%d", userID)
	return s.client.SCard(ctx, userTokenKey).Result()
}

// GetAllUserTokens returns all active token IDs for a user
func (s *RedisTokenStorage) GetAllUserTokens(ctx context.Context, userID int64) ([]string, error) {
	userTokenKey := fmt.Sprintf("user:tokens:%d", userID)
	return s.client.SMembers(ctx, userTokenKey).Result()
}

func (s *RedisTokenStorage) StoreTokenContext(ctx context.Context, tokenID string, userID int64, expiresAt time.Time, tokenCtx *Context) error {
	if tokenCtx == nil || !tokenCtx.Features["web_linked_device"] {
		return nil
	}

	deviceID := strings.TrimSpace(tokenCtx.LinkedDeviceID)
	if userID <= 0 || tokenID == "" || deviceID == "" {
		return nil
	}

	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil
	}

	meta := linkedTokenMetadata{
		UserID:         userID,
		LinkedDeviceID: deviceID,
		WebLinked:      true,
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	pipe := s.client.Pipeline()
	pipe.Set(ctx, linkedTokenMetadataKey(tokenID), payload, ttl)
	deviceTokensKey := linkedDeviceTokensKey(userID, deviceID)
	deviceIndexKey := linkedDeviceIndexKey(userID)
	pipe.SAdd(ctx, deviceTokensKey, tokenID)
	pipe.Expire(ctx, deviceTokensKey, ttl)
	pipe.SAdd(ctx, deviceIndexKey, deviceID)
	pipe.Expire(ctx, deviceIndexKey, ttl)

	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisTokenStorage) RevokeTokensByLinkedDevice(ctx context.Context, userID int64, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if userID <= 0 || deviceID == "" {
		return nil
	}

	deviceTokensKey := linkedDeviceTokensKey(userID, deviceID)
	tokenIDs, err := s.client.SMembers(ctx, deviceTokensKey).Result()
	if err != nil && err != redis.Nil {
		return err
	}

	for _, tokenID := range tokenIDs {
		if err := s.revokeTokenByID(ctx, userID, deviceID, tokenID); err != nil {
			return err
		}
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, deviceTokensKey)
	pipe.SRem(ctx, linkedDeviceIndexKey(userID), deviceID)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisTokenStorage) RevokeWebLinkedTokensExceptDevice(ctx context.Context, userID int64, keepDeviceID string) error {
	keepDeviceID = strings.TrimSpace(keepDeviceID)
	if userID <= 0 || keepDeviceID == "" {
		return nil
	}

	deviceIDs, err := s.client.SMembers(ctx, linkedDeviceIndexKey(userID)).Result()
	if err != nil && err != redis.Nil {
		return err
	}

	for _, deviceID := range deviceIDs {
		if strings.TrimSpace(deviceID) == "" || deviceID == keepDeviceID {
			continue
		}
		if err := s.RevokeTokensByLinkedDevice(ctx, userID, deviceID); err != nil {
			return err
		}
	}

	return nil
}

func (s *RedisTokenStorage) revokeTokenByID(ctx context.Context, userID int64, deviceID, tokenID string) error {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return nil
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, fmt.Sprintf("token:%s", tokenID))
	pipe.Del(ctx, linkedTokenMetadataKey(tokenID))
	pipe.SRem(ctx, fmt.Sprintf("user:tokens:%d", userID), tokenID)
	if strings.TrimSpace(deviceID) != "" {
		pipe.SRem(ctx, linkedDeviceTokensKey(userID, deviceID), tokenID)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisTokenStorage) cleanupLinkedTokenMetadata(ctx context.Context, tokenID string) error {
	metaKey := linkedTokenMetadataKey(tokenID)
	raw, err := s.client.Get(ctx, metaKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return err
	}

	var meta linkedTokenMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, metaKey)
	if meta.UserID > 0 && strings.TrimSpace(meta.LinkedDeviceID) != "" {
		deviceTokensKey := linkedDeviceTokensKey(meta.UserID, meta.LinkedDeviceID)
		pipe.SRem(ctx, deviceTokensKey, tokenID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	if meta.UserID > 0 && strings.TrimSpace(meta.LinkedDeviceID) != "" {
		deviceTokensKey := linkedDeviceTokensKey(meta.UserID, meta.LinkedDeviceID)
		count, err := s.client.SCard(ctx, deviceTokensKey).Result()
		if err != nil && err != redis.Nil {
			return err
		}
		if count == 0 {
			pipe := s.client.Pipeline()
			pipe.Del(ctx, deviceTokensKey)
			pipe.SRem(ctx, linkedDeviceIndexKey(meta.UserID), meta.LinkedDeviceID)
			if _, err := pipe.Exec(ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

func linkedTokenMetadataKey(tokenID string) string {
	return fmt.Sprintf("token_meta:%s", tokenID)
}

func linkedDeviceTokensKey(userID int64, deviceID string) string {
	return fmt.Sprintf("user:linked_device_tokens:%d:%s", userID, deviceID)
}

func linkedDeviceIndexKey(userID int64) string {
	return fmt.Sprintf("user:linked_devices:%d", userID)
}

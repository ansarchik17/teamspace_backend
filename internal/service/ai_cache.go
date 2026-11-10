package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"go.uber.org/zap"
)

// aiCacheTTL задаёт время жизни кэша для AI-специфичных данных.
// Неизменяемые данные (профили) кэшируются дольше,
// мутирующиеся (задачи, события) — короче.
const (
	aiCacheTTLUserProfile   = 10 * time.Minute
	aiCacheTTLCalendarToday = 60 * time.Second
	aiCacheTTLKanbanTasks   = 30 * time.Second
	aiCacheTTLContactSearch = 5 * time.Minute
	aiCacheTTLKanbanBoards  = 5 * time.Minute
)

// AICache — Redis-based TTL cache для AI Assistant endpoints.
// Инвалидация кэша происходит через явный InvalidateFor* методы,
// которые вызываются после мутирующих операций.
type AICache struct {
	redis  *redis.Client
	logger *zap.Logger
}

func NewAICache(redis *redis.Client, logger *zap.Logger) *AICache {
	return &AICache{
		redis:  redis,
		logger: logger.With(zap.String("component", "ai_cache")),
	}
}

// --------------------------------------------------------------------------
// Generic get/set
// --------------------------------------------------------------------------

func (c *AICache) Get(ctx context.Context, key string, dest interface{}) (bool, error) {
	if c.redis == nil {
		return false, nil
	}
	val, err := c.redis.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		c.logger.Warn("ai_cache_get_error", zap.String("key", key), zap.Error(err))
		return false, nil // soft failure — не ломаем запрос
	}
	if err := json.Unmarshal(val, dest); err != nil {
		c.logger.Warn("ai_cache_unmarshal_error", zap.String("key", key), zap.Error(err))
		return false, nil
	}
	return true, nil
}

func (c *AICache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) {
	if c.redis == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		c.logger.Warn("ai_cache_marshal_error", zap.String("key", key), zap.Error(err))
		return
	}
	if err := c.redis.Set(ctx, key, data, ttl).Err(); err != nil {
		c.logger.Warn("ai_cache_set_error", zap.String("key", key), zap.Error(err))
	}
}

func (c *AICache) SetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) {
	if c.redis == nil || len(values) == 0 {
		return
	}
	pipe := c.redis.Pipeline()
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			c.logger.Warn("ai_cache_marshal_error", zap.String("key", key), zap.Error(err))
			continue
		}
		pipe.Set(ctx, key, data, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		c.logger.Warn("ai_cache_set_many_error", zap.Int("key_count", len(values)), zap.Error(err))
	}
}

func (c *AICache) Delete(ctx context.Context, keys ...string) {
	if c.redis == nil || len(keys) == 0 {
		return
	}
	if err := c.redis.Del(ctx, keys...).Err(); err != nil {
		c.logger.Warn("ai_cache_delete_error", zap.Strings("keys", keys), zap.Error(err))
	}
}

func (c *AICache) GetInt64(ctx context.Context, key string) int64 {
	if c.redis == nil {
		return 0
	}
	value, err := c.redis.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0
	}
	if err != nil {
		c.logger.Warn("ai_cache_get_int64_error", zap.String("key", key), zap.Error(err))
		return 0
	}
	return value
}

func (c *AICache) Increment(ctx context.Context, key string) {
	if c.redis == nil {
		return
	}
	if err := c.redis.Incr(ctx, key).Err(); err != nil {
		c.logger.Warn("ai_cache_increment_error", zap.String("key", key), zap.Error(err))
	}
}

// --------------------------------------------------------------------------
// Cache keys
// --------------------------------------------------------------------------

func AICalendarTodayKey(userID int64, date string) string {
	return fmt.Sprintf("ai:cal:today:%d:%s", userID, date)
}

func AITasksSummaryKey(userID int64) string {
	return fmt.Sprintf("ai:tasks:summary:%d", userID)
}

func AIContactSearchKey(userID int64, query string) string {
	return fmt.Sprintf("ai:contacts:search:%d:%s", userID, query)
}

func AILocalizedKey(ctx context.Context, base string) string {
	return base + ":" + string(i18n.FromContext(ctx))
}

func aiCacheLocaleKeys(bases ...string) []string {
	keys := make([]string, 0, len(bases)*3)
	for _, base := range bases {
		keys = append(keys, base, base+":ru", base+":kk")
	}
	return keys
}

func AIKanbanBoardsKey(userID int64) string {
	return fmt.Sprintf("ai:kanban:boards:%d", userID)
}

// --------------------------------------------------------------------------
// Invalidation helpers — вызываются после мутирующих операций
// --------------------------------------------------------------------------

// InvalidateCalendarCache очищает кэш календаря пользователя.
// Вызывать после: create_calendar_event, delete_calendar_event.
func (c *AICache) InvalidateCalendarCache(ctx context.Context, userID int64) {
	// Удаляем кэш за сегодня и завтра (события могут быть на граничных датах)
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	c.Delete(ctx, aiCacheLocaleKeys(
		AICalendarTodayKey(userID, today),
		AICalendarTodayKey(userID, tomorrow),
	)...)
	c.logger.Debug("ai_cache_invalidated_calendar", zap.Int64("user_id", userID))
}

// InvalidateTasksCache очищает кэш задач пользователя.
// Вызывать после: create_kanban_task, close_kanban_task, move_task.
func (c *AICache) InvalidateTasksCache(ctx context.Context, userID int64) {
	c.Delete(ctx, aiCacheLocaleKeys(AITasksSummaryKey(userID))...)
	c.logger.Debug("ai_cache_invalidated_tasks", zap.Int64("user_id", userID))
}

// --------------------------------------------------------------------------
// TTL constants — экспортируются для использования в handlers
// --------------------------------------------------------------------------

func AICalendarTodayTTL() time.Duration { return aiCacheTTLCalendarToday }
func AITasksSummaryTTL() time.Duration  { return aiCacheTTLKanbanTasks }
func AIContactSearchTTL() time.Duration { return aiCacheTTLContactSearch }
func AIKanbanBoardsTTL() time.Duration  { return aiCacheTTLKanbanBoards }

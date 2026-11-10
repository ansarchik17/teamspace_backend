-- +goose NO TRANSACTION
-- +goose Up
-- Индекс для быстрого поиска событий конкретного владельца
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_events_user_id ON events (user_id);

-- Индекс для фильтрации по времени (диапазон дат) - критично для календаря
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_events_time_range ON events (start_time, end_time);

-- Индекс для таблицы участников, чтобы быстро находить, куда приглашен юзер
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_event_attendees_user_status ON event_attendees (user_id, status);

-- Индекс для связки таблиц (JOIN)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_event_attendees_event_id ON event_attendees (event_id);

-- Индекс для мягкого удаления (чтобы быстро отсекать удаленные)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_events_deleted_at ON events (deleted_at) WHERE deleted_at IS NULL;

-- +goose Down
-- +goose NO TRANSACTION
DROP INDEX IF EXISTS idx_events_user_id;
DROP INDEX IF EXISTS idx_events_time_range;
DROP INDEX IF EXISTS idx_event_attendees_user_status;
DROP INDEX IF EXISTS idx_event_attendees_event_id;
DROP INDEX IF EXISTS idx_events_deleted_at;
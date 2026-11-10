-- +goose Up
-- +goose NO TRANSACTION

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_title_trgm
    ON tasks USING gin (title gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_status_trgm
    ON tasks USING gin (status gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_events_title_trgm
    ON events USING gin (title gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_events_location_trgm
    ON events USING gin (location gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_files_real_name_trgm
    ON files USING gin (real_name gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_folders_name_trgm
    ON folders USING gin (name gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_event_attendees_user_id
    ON event_attendees(user_id);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_event_attendees_user_id;
DROP INDEX CONCURRENTLY IF EXISTS idx_folders_name_trgm;
DROP INDEX CONCURRENTLY IF EXISTS idx_files_real_name_trgm;
DROP INDEX CONCURRENTLY IF EXISTS idx_events_location_trgm;
DROP INDEX CONCURRENTLY IF EXISTS idx_events_title_trgm;
DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_status_trgm;
DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_title_trgm;

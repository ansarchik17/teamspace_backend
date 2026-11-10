-- +goose Up
-- +goose NO TRANSACTION

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_calendar_search_trgm
    ON users USING gin (
        LOWER(
            regexp_replace(
                TRIM(
                    regexp_replace(
                        COALESCE(first_name, '') || ' ' ||
                        COALESCE(last_name, '') || ' ' ||
                        COALESCE(full_name_local, '') || ' ' ||
                        COALESCE(username, '') || ' ' ||
                        COALESCE(email, ''),
                        '[‐‑‒–—―-]+', ' ', 'g'
                    )
                ),
                '\s+', ' ', 'g'
            )
        ) gin_trgm_ops
    )
    WHERE deleted_at IS NULL AND is_active = true;

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_users_calendar_search_trgm;

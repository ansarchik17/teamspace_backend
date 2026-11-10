-- +goose Up
CREATE INDEX IF NOT EXISTS idx_users_lookup_lower_email
    ON users (LOWER(TRIM(email)))
    WHERE deleted_at IS NULL AND email IS NOT NULL AND TRIM(email) <> '';

CREATE INDEX IF NOT EXISTS idx_users_lookup_normalized_username
    ON users (LOWER(REGEXP_REPLACE(TRIM(username), '\s+', '', 'g')))
    WHERE deleted_at IS NULL AND username IS NOT NULL AND TRIM(username) <> '';

CREATE INDEX IF NOT EXISTS idx_users_merged_into_id
    ON users (merged_into_id)
    WHERE merged_into_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_users_lookup_lower_email;
DROP INDEX IF EXISTS idx_users_lookup_normalized_username;
DROP INDEX IF EXISTS idx_users_merged_into_id;

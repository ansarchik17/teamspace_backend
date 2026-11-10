-- +goose Up
-- Drop unique constraint on username
DROP INDEX IF EXISTS idx_users_username;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_key;

-- Create non-unique index for performance
CREATE INDEX idx_users_username ON users(username) WHERE deleted_at IS NULL;

-- +goose Down
-- Cannot easily restore unique constraint if duplicates exist
-- Just drop index and try to recreate unique constraint/index
DROP INDEX IF EXISTS idx_users_username;
ALTER TABLE users ADD CONSTRAINT users_username_key UNIQUE (username);
CREATE INDEX idx_users_username ON users(username) WHERE deleted_at IS NULL;

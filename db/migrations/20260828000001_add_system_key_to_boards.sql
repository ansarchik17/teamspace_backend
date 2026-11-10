-- +goose Up
ALTER TABLE boards ADD COLUMN IF NOT EXISTS system_key text;

-- +goose Down
ALTER TABLE boards DROP COLUMN IF EXISTS system_key;

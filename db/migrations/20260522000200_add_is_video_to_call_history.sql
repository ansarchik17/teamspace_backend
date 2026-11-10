-- +goose Up
ALTER TABLE call_history
    ADD COLUMN IF NOT EXISTS is_video BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE call_history
    DROP COLUMN IF EXISTS is_video;
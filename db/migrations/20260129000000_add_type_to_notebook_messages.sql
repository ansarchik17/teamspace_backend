-- +goose Up
ALTER TABLE notebook_messages ADD COLUMN IF NOT EXISTS type TEXT NOT NULL DEFAULT 'text';

-- +goose Down
ALTER TABLE notebook_messages DROP COLUMN IF EXISTS type;

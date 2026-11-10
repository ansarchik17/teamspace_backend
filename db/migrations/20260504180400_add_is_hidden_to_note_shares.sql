-- +goose Up
ALTER TABLE note_shares ADD COLUMN IF NOT EXISTS is_hidden BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE note_shares DROP COLUMN IF EXISTS is_hidden;

-- +goose Up
ALTER TABLE note_folders
ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT '#1687FF';

-- +goose Down
ALTER TABLE note_folders
DROP COLUMN IF EXISTS color;

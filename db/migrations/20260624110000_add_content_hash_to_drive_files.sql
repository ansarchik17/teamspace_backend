-- +goose Up
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_files_drive_content_hash
    ON files (content_hash)
    WHERE deleted_at IS NULL AND scope = 'drive' AND content_hash IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_files_drive_content_hash;

ALTER TABLE files
    DROP COLUMN IF EXISTS content_hash;

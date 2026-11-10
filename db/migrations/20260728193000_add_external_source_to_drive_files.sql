-- +goose Up
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS source_system_code VARCHAR(100),
    ADD COLUMN IF NOT EXISTS source_system_name VARCHAR(255),
    ADD COLUMN IF NOT EXISTS external_message_id VARCHAR(255);

CREATE INDEX IF NOT EXISTS idx_files_source_system_code
    ON files (source_system_code)
    WHERE deleted_at IS NULL AND scope = 'drive' AND source_system_code IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_files_source_external_message
    ON files (source_system_code, external_message_id)
    WHERE deleted_at IS NULL AND scope = 'drive'
      AND source_system_code IS NOT NULL
      AND external_message_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_files_source_external_message;
DROP INDEX IF EXISTS idx_files_source_system_code;

ALTER TABLE files
    DROP COLUMN IF EXISTS external_message_id,
    DROP COLUMN IF EXISTS source_system_name,
    DROP COLUMN IF EXISTS source_system_code;

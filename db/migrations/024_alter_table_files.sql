-- +goose Up
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS onlyoffice_key TEXT;

CREATE INDEX IF NOT EXISTS ix_files_onlyoffice_key ON files(onlyoffice_key);

-- +goose Down
DROP INDEX IF EXISTS ix_files_onlyoffice_key;

ALTER TABLE files
    DROP COLUMN IF EXISTS onlyoffice_key;
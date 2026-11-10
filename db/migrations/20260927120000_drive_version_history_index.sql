-- +goose Up
CREATE UNIQUE INDEX idx_file_versions_file_version ON file_versions(file_id, version DESC);
CREATE INDEX idx_drive_files_trash_expiry ON files(owner_id, deleted_at) WHERE deleted_at IS NOT NULL AND scope = 'drive';
CREATE INDEX idx_drive_folders_trash_expiry ON folders(owner_id, deleted_at) WHERE deleted_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_drive_folders_trash_expiry;
DROP INDEX IF EXISTS idx_drive_files_trash_expiry;
DROP INDEX IF EXISTS idx_file_versions_file_version;

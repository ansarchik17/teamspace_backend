-- +goose Up
CREATE TABLE IF NOT EXISTS drive_activity_logs (
    id UUID PRIMARY KEY,
    action VARCHAR(32) NOT NULL CHECK (action IN ('created', 'uploaded', 'renamed', 'moved', 'edited', 'deleted', 'copied')),
    resource_type VARCHAR(20) NOT NULL CHECK (resource_type IN ('file', 'folder')),
    resource_id UUID NOT NULL,
    resource_name TEXT NOT NULL,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id UUID NULL REFERENCES folders(id) ON DELETE SET NULL,
    from_folder_id UUID NULL REFERENCES folders(id) ON DELETE SET NULL,
    to_folder_id UUID NULL REFERENCES folders(id) ON DELETE SET NULL,
    resource_path TEXT NULL,
    from_path TEXT NULL,
    to_path TEXT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_drive_activity_logs_owner_created_at
    ON drive_activity_logs (owner_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_drive_activity_logs_resource
    ON drive_activity_logs (resource_type, resource_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_drive_activity_logs_folder_created_at
    ON drive_activity_logs (folder_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_drive_activity_logs_from_folder_created_at
    ON drive_activity_logs (from_folder_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_drive_activity_logs_to_folder_created_at
    ON drive_activity_logs (to_folder_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_drive_activity_logs_to_folder_created_at;
DROP INDEX IF EXISTS idx_drive_activity_logs_from_folder_created_at;
DROP INDEX IF EXISTS idx_drive_activity_logs_folder_created_at;
DROP INDEX IF EXISTS idx_drive_activity_logs_resource;
DROP INDEX IF EXISTS idx_drive_activity_logs_owner_created_at;
DROP TABLE IF EXISTS drive_activity_logs;

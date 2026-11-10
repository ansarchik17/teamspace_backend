-- +goose Up
CREATE TABLE IF NOT EXISTS drive_delete_requests (
    id UUID PRIMARY KEY,
    resource_type VARCHAR(16) NOT NULL CHECK (resource_type IN ('file', 'folder')),
    resource_id UUID NOT NULL,
    resource_name VARCHAR(255) NOT NULL,
    parent_folder_id UUID NULL REFERENCES folders(id) ON DELETE SET NULL,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    requested_by BIGINT NOT NULL REFERENCES users(id),
    resolved_by BIGINT NULL REFERENCES users(id),
    recursive BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    rejection_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ NULL,
    executed_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_drive_delete_requests_owner_status
    ON drive_delete_requests (owner_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_drive_delete_requests_requester_status
    ON drive_delete_requests (requested_by, status, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS ux_drive_delete_requests_pending_unique
    ON drive_delete_requests (resource_type, resource_id, requested_by)
    WHERE status = 'pending';

-- +goose Down
DROP INDEX IF EXISTS ux_drive_delete_requests_pending_unique;
DROP INDEX IF EXISTS idx_drive_delete_requests_requester_status;
DROP INDEX IF EXISTS idx_drive_delete_requests_owner_status;
DROP TABLE IF EXISTS drive_delete_requests;

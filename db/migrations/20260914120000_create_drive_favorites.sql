-- +goose Up
CREATE TABLE IF NOT EXISTS drive_favorites (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('file', 'folder')),
    resource_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, resource_type, resource_id)
);

CREATE INDEX IF NOT EXISTS idx_drive_favorites_user_created
    ON drive_favorites (user_id, created_at DESC);

ALTER TABLE drive_activity_logs
    DROP CONSTRAINT IF EXISTS drive_activity_logs_action_check;

ALTER TABLE drive_activity_logs
    ADD CONSTRAINT drive_activity_logs_action_check
    CHECK (action IN ('created', 'uploaded', 'renamed', 'moved', 'edited', 'deleted', 'copied', 'restored'));

-- +goose Down
ALTER TABLE drive_activity_logs
    DROP CONSTRAINT IF EXISTS drive_activity_logs_action_check;

ALTER TABLE drive_activity_logs
    ADD CONSTRAINT drive_activity_logs_action_check
    CHECK (action IN ('created', 'uploaded', 'renamed', 'moved', 'edited', 'deleted', 'copied'));

DROP INDEX IF EXISTS idx_drive_favorites_user_created;
DROP TABLE IF EXISTS drive_favorites;

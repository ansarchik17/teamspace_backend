-- +goose Up
-- Both features previously used version 20260929000800 on separate branches.
-- Reconcile databases that applied either version before the branches merged.
CREATE INDEX IF NOT EXISTS idx_notes_user_trash_deleted
    ON notes(user_id, deleted_at DESC, id DESC)
    WHERE is_deleted = TRUE;

CREATE TABLE IF NOT EXISTS drive_wopi_locks (
    file_id uuid PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    lock_value text NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_drive_wopi_locks_expiry ON drive_wopi_locks(expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_drive_wopi_locks_expiry;
DROP TABLE IF EXISTS drive_wopi_locks;

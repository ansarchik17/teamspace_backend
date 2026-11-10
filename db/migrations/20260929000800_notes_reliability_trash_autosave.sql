-- +goose Up
-- NTS-003: Reliability - Trash indexing for fast deleted note retrieval and retention queries

CREATE INDEX IF NOT EXISTS idx_notes_user_trash_deleted
    ON notes(user_id, deleted_at DESC, id DESC)
    WHERE is_deleted = TRUE;

-- +goose Down
DROP INDEX IF EXISTS idx_notes_user_trash_deleted;

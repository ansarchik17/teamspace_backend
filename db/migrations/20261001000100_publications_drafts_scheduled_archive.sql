-- +goose Up
-- +goose StatementBegin
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS archive_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_publications_archive_at
    ON publications(archive_at)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_publications_status_archived
    ON publications(status, archived_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_publications_status_author
    ON publications(status, author_id)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_publications_status_author;
DROP INDEX IF EXISTS idx_publications_status_archived;
DROP INDEX IF EXISTS idx_publications_archive_at;

ALTER TABLE publications
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS archive_at;
-- +goose StatementEnd

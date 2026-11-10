-- +goose Up
-- +goose StatementBegin
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS cover_image_url TEXT,
    ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'published',
    ADD COLUMN IF NOT EXISTS content_doc JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS content_html TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'check_publications_status'
    ) THEN
        ALTER TABLE publications
            ADD CONSTRAINT check_publications_status
            CHECK (status IN ('draft', 'published', 'scheduled', 'archived'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_publications_status_published_at
    ON publications(status, published_at DESC)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_publications_status_published_at;
ALTER TABLE publications
    DROP CONSTRAINT IF EXISTS check_publications_status,
    DROP COLUMN IF EXISTS content_html,
    DROP COLUMN IF EXISTS content_doc,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS cover_image_url;
-- +goose StatementEnd

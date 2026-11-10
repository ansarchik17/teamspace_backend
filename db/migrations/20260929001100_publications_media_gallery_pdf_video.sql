-- +goose Up
-- +goose StatementBegin
ALTER TABLE publication_attachments
    ADD COLUMN IF NOT EXISTS caption TEXT,
    ADD COLUMN IF NOT EXISTS order_index INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS mime_type VARCHAR(100),
    ADD COLUMN IF NOT EXISTS thumbnail_url TEXT,
    ADD COLUMN IF NOT EXISTS video_provider VARCHAR(50),
    ADD COLUMN IF NOT EXISTS video_embed_url TEXT;

CREATE INDEX IF NOT EXISTS idx_publication_attachments_order
    ON publication_attachments(publication_id, order_index ASC, created_at ASC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_publication_attachments_order;
ALTER TABLE publication_attachments
    DROP COLUMN IF EXISTS video_embed_url,
    DROP COLUMN IF EXISTS video_provider,
    DROP COLUMN IF EXISTS thumbnail_url,
    DROP COLUMN IF EXISTS mime_type,
    DROP COLUMN IF EXISTS order_index,
    DROP COLUMN IF EXISTS caption;
-- +goose StatementEnd

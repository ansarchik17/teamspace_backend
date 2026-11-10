-- +goose Up
-- +goose StatementBegin
ALTER TABLE channel_posts
    ADD COLUMN IF NOT EXISTS reply_to_id UUID REFERENCES channel_posts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_channel_posts_reply_to
    ON channel_posts(reply_to_id, created_at ASC)
    WHERE reply_to_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_channel_posts_reply_to;
ALTER TABLE channel_posts DROP COLUMN IF EXISTS reply_to_id;
-- +goose StatementEnd

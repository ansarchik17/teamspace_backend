-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_post_mentions (
    post_id UUID NOT NULL REFERENCES channel_posts(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_post_mentions_user
    ON channel_post_mentions(user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_post_mentions;
-- +goose StatementEnd

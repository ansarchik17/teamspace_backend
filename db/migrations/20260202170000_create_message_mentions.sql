-- +goose Up
-- +goose StatementBegin

-- Table to track user mentions in messages
CREATE TABLE message_mentions (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (message_id, user_id)
);

-- Index for fast lookup of mentions for a specific user (to show the @ badge)
CREATE INDEX idx_message_mentions_user ON message_mentions(user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS message_mentions;
-- +goose StatementEnd

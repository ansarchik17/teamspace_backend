-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS user_mailboxes (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mailbox_login TEXT NOT NULL,
    is_primary BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_mailboxes_mailbox_login_unique
    ON user_mailboxes (LOWER(mailbox_login));

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_mailboxes_primary_per_user
    ON user_mailboxes (user_id)
    WHERE is_primary = TRUE;

CREATE INDEX IF NOT EXISTS idx_user_mailboxes_user_id
    ON user_mailboxes (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_mailboxes;
-- +goose StatementEnd

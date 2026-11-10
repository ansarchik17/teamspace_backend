-- +goose Up
CREATE TABLE IF NOT EXISTS user_qr_tokens (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_qr_tokens_user_id_created_at
    ON user_qr_tokens(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_qr_tokens_expires_at
    ON user_qr_tokens(expires_at);

-- +goose Down
DROP TABLE IF EXISTS user_qr_tokens;

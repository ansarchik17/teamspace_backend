-- +goose Up

ALTER TABLE user_public_keys
    ADD COLUMN IF NOT EXISTS linked_ip VARCHAR(64),
    ADD COLUMN IF NOT EXISTS linked_country VARCHAR(120),
    ADD COLUMN IF NOT EXISTS linked_city VARCHAR(120),
    ADD COLUMN IF NOT EXISTS last_ip VARCHAR(64),
    ADD COLUMN IF NOT EXISTS last_country VARCHAR(120),
    ADD COLUMN IF NOT EXISTS last_city VARCHAR(120),
    ADD COLUMN IF NOT EXISTS last_user_agent TEXT,
    ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;

ALTER TABLE web_pairing_sessions
    ADD COLUMN IF NOT EXISTS request_ip VARCHAR(64),
    ADD COLUMN IF NOT EXISTS request_country VARCHAR(120),
    ADD COLUMN IF NOT EXISTS request_city VARCHAR(120),
    ADD COLUMN IF NOT EXISTS request_user_agent TEXT;

CREATE INDEX IF NOT EXISTS idx_user_public_keys_last_login_at
    ON user_public_keys(user_id, last_login_at DESC NULLS LAST);

-- +goose Down

DROP INDEX IF EXISTS idx_user_public_keys_last_login_at;

ALTER TABLE web_pairing_sessions
    DROP COLUMN IF EXISTS request_user_agent,
    DROP COLUMN IF EXISTS request_city,
    DROP COLUMN IF EXISTS request_country,
    DROP COLUMN IF EXISTS request_ip;

ALTER TABLE user_public_keys
    DROP COLUMN IF EXISTS last_login_at,
    DROP COLUMN IF EXISTS last_user_agent,
    DROP COLUMN IF EXISTS last_city,
    DROP COLUMN IF EXISTS last_country,
    DROP COLUMN IF EXISTS last_ip,
    DROP COLUMN IF EXISTS linked_city,
    DROP COLUMN IF EXISTS linked_country,
    DROP COLUMN IF EXISTS linked_ip;

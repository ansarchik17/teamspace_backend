-- +goose Up

ALTER TABLE user_public_keys
    ADD COLUMN IF NOT EXISTS device_name VARCHAR(255),
    ADD COLUMN IF NOT EXISTS platform VARCHAR(50),
    ADD COLUMN IF NOT EXISTS status VARCHAR(32) NOT NULL DEFAULT 'trusted',
    ADD COLUMN IF NOT EXISTS trusted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_user_public_keys_status'
    ) THEN
        ALTER TABLE user_public_keys
            ADD CONSTRAINT chk_user_public_keys_status
            CHECK (status IN ('pending', 'trusted', 'revoked'));
    END IF;
END $$;
-- +goose StatementEnd

UPDATE user_public_keys
SET trusted_at = COALESCE(trusted_at, created_at)
WHERE status = 'trusted' AND trusted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_public_keys_user_status
    ON user_public_keys(user_id, status)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS web_pairing_sessions (
    id UUID PRIMARY KEY,
    qr_token_hash TEXT NOT NULL,
    session_token_hash TEXT NOT NULL,
    login_code_hash TEXT NULL,
    user_id BIGINT NULL REFERENCES users(id) ON DELETE CASCADE,
    web_device_id VARCHAR(255) NOT NULL,
    web_public_key TEXT NOT NULL,
    web_ephemeral_pub TEXT NULL,
    device_name VARCHAR(255) NULL,
    platform VARCHAR(50) NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    approved_at TIMESTAMPTZ NULL,
    synced_at TIMESTAMPTZ NULL,
    redeemed_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_web_pairing_sessions_status'
    ) THEN
        ALTER TABLE web_pairing_sessions
            ADD CONSTRAINT chk_web_pairing_sessions_status
            CHECK (status IN ('pending', 'approved', 'synced', 'redeemed', 'expired', 'revoked'));
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS ux_web_pairing_sessions_session_token_hash
    ON web_pairing_sessions(session_token_hash);

CREATE INDEX IF NOT EXISTS idx_web_pairing_sessions_status_expires
    ON web_pairing_sessions(status, expires_at);

CREATE INDEX IF NOT EXISTS idx_web_pairing_sessions_user_id
    ON web_pairing_sessions(user_id);

-- +goose Down

DROP INDEX IF EXISTS idx_web_pairing_sessions_user_id;
DROP INDEX IF EXISTS idx_web_pairing_sessions_status_expires;
DROP INDEX IF EXISTS ux_web_pairing_sessions_session_token_hash;

ALTER TABLE web_pairing_sessions
    DROP CONSTRAINT IF EXISTS chk_web_pairing_sessions_status;

DROP TABLE IF EXISTS web_pairing_sessions;

DROP INDEX IF EXISTS idx_user_public_keys_user_status;

ALTER TABLE user_public_keys
    DROP CONSTRAINT IF EXISTS chk_user_public_keys_status;

ALTER TABLE user_public_keys
    DROP COLUMN IF EXISTS last_seen_at,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS trusted_at,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS platform,
    DROP COLUMN IF EXISTS device_name;

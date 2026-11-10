-- +goose Up
CREATE TABLE IF NOT EXISTS quick_pin_credential (
                                                    id BIGSERIAL PRIMARY KEY,
                                                    user_id BIGINT NOT NULL REFERENCES users(id),
    device_id VARCHAR(100) NOT NULL,
    pin_hash VARCHAR(200) NOT NULL,
    encrypted_session_secret TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_quick_pin_user_device UNIQUE (user_id, device_id)
    );

CREATE INDEX IF NOT EXISTS idx_quick_pin_user_device_active
    ON quick_pin_credential(user_id, device_id, active);

-- +goose Down
DROP TABLE IF EXISTS quick_pin_credential;
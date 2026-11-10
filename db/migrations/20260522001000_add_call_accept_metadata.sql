-- +goose Up
ALTER TABLE call_history
    ADD COLUMN IF NOT EXISTS accepted_by_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS accepted_by_device_id TEXT,
    ADD COLUMN IF NOT EXISTS accepted_by_session_id TEXT,
    ADD COLUMN IF NOT EXISTS accepted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_call_history_accepted_by_user_id
    ON call_history (accepted_by_user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_call_history_accepted_by_user_id;

ALTER TABLE call_history
    DROP COLUMN IF EXISTS accepted_at,
    DROP COLUMN IF EXISTS accepted_by_session_id,
    DROP COLUMN IF EXISTS accepted_by_device_id,
    DROP COLUMN IF EXISTS accepted_by_user_id;

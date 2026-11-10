-- +goose Up
ALTER TABLE call_history
    ADD COLUMN IF NOT EXISTS call_type text NOT NULL DEFAULT 'private';

UPDATE call_history
SET call_type = 'group'
WHERE to_user_id = 0
   OR room_id LIKE 'call_group_%';

CREATE INDEX IF NOT EXISTS idx_call_history_call_type
    ON call_history (call_type, updated_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_call_history_call_type;

ALTER TABLE call_history
    DROP COLUMN IF EXISTS call_type;

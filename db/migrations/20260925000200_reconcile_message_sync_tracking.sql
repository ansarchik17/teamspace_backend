-- +goose Up
-- Reconcile installations where version 20260924000500 actually applied BRD-007.
-- MSG-008: Network reliability - connection recovery and message synchronization

CREATE TABLE IF NOT EXISTS message_sync_cursors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id VARCHAR(255) NOT NULL,
    conversation_id UUID,
    last_sequence_number BIGINT NOT NULL DEFAULT 0,
    last_sync_timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT message_sync_cursors_sequence_nonnegative CHECK (last_sequence_number >= 0)
);

-- PostgreSQL UNIQUE treats NULLs as distinct, so global cursors need a separate
-- partial index to guarantee one row per user/device.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sync_cursors_global_unique
    ON message_sync_cursors(user_id, device_id)
    WHERE conversation_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_sync_cursors_conversation_unique
    ON message_sync_cursors(user_id, device_id, conversation_id)
    WHERE conversation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sync_cursors_user_device
    ON message_sync_cursors(user_id, device_id);
CREATE INDEX IF NOT EXISTS idx_sync_cursors_updated_at
    ON message_sync_cursors(updated_at);

ALTER TABLE messages ADD COLUMN IF NOT EXISTS sequence_number BIGSERIAL;
CREATE INDEX IF NOT EXISTS idx_messages_sequence_number
    ON messages(sequence_number) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_messages_conv_sequence
    ON messages(conversation_id, sequence_number) WHERE is_deleted = false;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION update_message_sync_cursor_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at := CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Keep this safe if an earlier failed/manual deployment already created it.
DROP TRIGGER IF EXISTS trigger_update_message_sync_cursor_timestamp ON message_sync_cursors;
CREATE TRIGGER trigger_update_message_sync_cursor_timestamp
    BEFORE UPDATE ON message_sync_cursors
    FOR EACH ROW
    EXECUTE FUNCTION update_message_sync_cursor_timestamp();

-- +goose Down
-- Forward-only repair; preserve objects owned by the original MSG-008 migration.
SELECT 1;

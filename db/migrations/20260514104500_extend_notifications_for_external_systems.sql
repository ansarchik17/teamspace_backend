-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS source_system_code VARCHAR(100),
    ADD COLUMN IF NOT EXISTS external_message_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS notification_status VARCHAR(30) NOT NULL DEFAULT 'PROCESSED',
    ADD COLUMN IF NOT EXISTS push_status VARCHAR(30) NOT NULL DEFAULT 'SKIPPED',
    ADD COLUMN IF NOT EXISTS error_message TEXT,
    ADD COLUMN IF NOT EXISTS sent_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP NOT NULL DEFAULT NOW();

CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_source_external_message
    ON notifications (source_system_code, external_message_id)
    WHERE source_system_code IS NOT NULL AND external_message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_notifications_source_system_code
    ON notifications (source_system_code);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_source_system_code;
DROP INDEX IF EXISTS idx_notifications_source_external_message;

ALTER TABLE notifications
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS sent_at,
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS push_status,
    DROP COLUMN IF EXISTS notification_status,
    DROP COLUMN IF EXISTS external_message_id,
    DROP COLUMN IF EXISTS source_system_code;
-- +goose StatementEnd
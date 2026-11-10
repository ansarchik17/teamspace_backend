-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS short_text TEXT,
    ADD COLUMN IF NOT EXISTS source_system_name VARCHAR(255),
    ADD COLUMN IF NOT EXISTS notification_type VARCHAR(100),
    ADD COLUMN IF NOT EXISTS application_number VARCHAR(255),
    ADD COLUMN IF NOT EXISTS action_url TEXT,
    ADD COLUMN IF NOT EXISTS processed_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notifications
    DROP COLUMN IF EXISTS processed_at,
    DROP COLUMN IF EXISTS action_url,
    DROP COLUMN IF EXISTS application_number,
    DROP COLUMN IF EXISTS notification_type,
    DROP COLUMN IF EXISTS source_system_name,
    DROP COLUMN IF EXISTS short_text;
-- +goose StatementEnd

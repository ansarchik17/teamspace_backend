-- +goose Up
-- Calendar release migrations follow the deployed main migration sequence.
-- +goose StatementBegin

-- Add timezone column to events table with default Asia/Almaty
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS timezone VARCHAR(100) NOT NULL DEFAULT 'Asia/Almaty';

CREATE INDEX IF NOT EXISTS idx_events_timezone ON events(timezone);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_events_timezone;
ALTER TABLE events DROP COLUMN IF EXISTS timezone;

-- +goose StatementEnd

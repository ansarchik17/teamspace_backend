-- +goose Up
-- +goose StatementBegin

-- Add Apple Calendar integration fields to users table
ALTER TABLE users 
ADD COLUMN apple_calendar_email TEXT,
ADD COLUMN apple_app_specific_password TEXT,
ADD COLUMN apple_calendar_sync_enabled BOOLEAN DEFAULT FALSE,
ADD COLUMN apple_calendar_id TEXT;

-- Table for storing Apple Calendar event mapping
CREATE TABLE apple_calendar_event_mappings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    apple_event_id VARCHAR(255) NOT NULL,
    apple_calendar_id TEXT NOT NULL,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(event_id, user_id),
    UNIQUE(apple_event_id, user_id)
);

CREATE INDEX idx_apple_calendar_mappings_event_id ON apple_calendar_event_mappings(event_id);
CREATE INDEX idx_apple_calendar_mappings_user_id ON apple_calendar_event_mappings(user_id);
CREATE INDEX idx_apple_calendar_mappings_apple_event_id ON apple_calendar_event_mappings(apple_event_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS apple_calendar_event_mappings;

ALTER TABLE users 
DROP COLUMN IF EXISTS apple_calendar_email,
DROP COLUMN IF EXISTS apple_app_specific_password,
DROP COLUMN IF EXISTS apple_calendar_sync_enabled,
DROP COLUMN IF EXISTS apple_calendar_id;

-- +goose StatementEnd

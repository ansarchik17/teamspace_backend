-- +goose Up
-- +goose StatementBegin

-- Add Google Calendar integration fields to users table
ALTER TABLE users 
ADD COLUMN google_access_token TEXT,
ADD COLUMN google_refresh_token TEXT,
ADD COLUMN google_token_expiry TIMESTAMPTZ,
ADD COLUMN google_calendar_sync_enabled BOOLEAN DEFAULT FALSE,
ADD COLUMN google_calendar_id VARCHAR(255);

-- Table for storing Google Calendar event mapping
CREATE TABLE google_calendar_event_mappings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    google_event_id VARCHAR(255) NOT NULL,
    google_calendar_id VARCHAR(255) NOT NULL,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(event_id, user_id),
    UNIQUE(google_event_id, user_id)
);

CREATE INDEX idx_google_calendar_mappings_event_id ON google_calendar_event_mappings(event_id);
CREATE INDEX idx_google_calendar_mappings_user_id ON google_calendar_event_mappings(user_id);
CREATE INDEX idx_google_calendar_mappings_google_event_id ON google_calendar_event_mappings(google_event_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS google_calendar_event_mappings;

ALTER TABLE users 
DROP COLUMN IF EXISTS google_access_token,
DROP COLUMN IF EXISTS google_refresh_token,
DROP COLUMN IF EXISTS google_token_expiry,
DROP COLUMN IF EXISTS google_calendar_sync_enabled,
DROP COLUMN IF EXISTS google_calendar_id;

-- +goose StatementEnd

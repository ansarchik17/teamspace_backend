-- +goose Up
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS meeting_provider TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS meeting_room_id TEXT,
    ADD COLUMN IF NOT EXISTS meeting_url TEXT,
    ADD COLUMN IF NOT EXISTS livekit_room_name TEXT,
    ADD COLUMN IF NOT EXISTS cisco_meeting_id TEXT,
    ADD COLUMN IF NOT EXISTS cisco_join_url TEXT,
    ADD COLUMN IF NOT EXISTS online_meeting_status TEXT NOT NULL DEFAULT 'none';

-- +goose Down
ALTER TABLE events
    DROP COLUMN IF EXISTS online_meeting_status,
    DROP COLUMN IF EXISTS cisco_join_url,
    DROP COLUMN IF EXISTS cisco_meeting_id,
    DROP COLUMN IF EXISTS livekit_room_name,
    DROP COLUMN IF EXISTS meeting_url,
    DROP COLUMN IF EXISTS meeting_room_id,
    DROP COLUMN IF EXISTS meeting_provider;

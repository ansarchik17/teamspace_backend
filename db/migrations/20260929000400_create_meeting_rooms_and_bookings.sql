-- +goose Up
-- Calendar release migrations follow the deployed main migration sequence.
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE IF NOT EXISTS meeting_rooms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    capacity INT NOT NULL DEFAULT 4 CHECK (capacity > 0),
    floor INT,
    location VARCHAR(255),
    color VARCHAR(50) DEFAULT '#4F46E5',
    equipment TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_meeting_rooms_active ON meeting_rooms(is_active) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_meeting_rooms_name ON meeting_rooms(name) WHERE deleted_at IS NULL;

-- Alter events table to link to meeting_rooms
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS room_id UUID REFERENCES meeting_rooms(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_events_room_id ON events(room_id) WHERE deleted_at IS NULL;

-- Room Bookings table with atomic exclusion constraint to prevent double-booking
CREATE TABLE IF NOT EXISTS room_bookings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id UUID NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    event_id UUID NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'confirmed' CHECK (status IN ('confirmed','cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NULL,

    CONSTRAINT chk_room_booking_times CHECK (end_time > start_time)
);

CREATE INDEX IF NOT EXISTS idx_room_bookings_room_time ON room_bookings(room_id, start_time, end_time) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_room_bookings_event_id ON room_bookings(event_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_room_bookings_user_id ON room_bookings(user_id) WHERE deleted_at IS NULL;

-- Exclusion constraint guaranteeing zero double-booking race conditions at DB level
ALTER TABLE room_bookings
    ADD CONSTRAINT no_overlapping_room_bookings
    EXCLUDE USING gist (
        room_id WITH =,
        tstzrange(start_time, end_time, '[)') WITH &&
    )
    WHERE (deleted_at IS NULL AND status = 'confirmed');

-- Seed standard corporate meeting rooms (idempotent)
INSERT INTO meeting_rooms (name, description, capacity, floor, location, color, equipment, is_active)
SELECT 'Переговорная Алматы', 'Основная большая переговорная с ТВ и ВКС', 12, 3, 'Блок А, 3 этаж, каб. 301', '#3B82F6', ARRAY['tv', 'projector', 'whiteboard', 'video_call']::TEXT[], TRUE
WHERE NOT EXISTS (SELECT 1 FROM meeting_rooms WHERE name = 'Переговорная Алматы');

INSERT INTO meeting_rooms (name, description, capacity, floor, location, color, equipment, is_active)
SELECT 'Переговорная Астана', 'Зал совещаний с системой конференц-связи', 18, 3, 'Блок А, 3 этаж, каб. 305', '#10B981', ARRAY['tv', 'video_call', 'conference_phone', 'whiteboard']::TEXT[], TRUE
WHERE NOT EXISTS (SELECT 1 FROM meeting_rooms WHERE name = 'Переговорная Астана');

INSERT INTO meeting_rooms (name, description, capacity, floor, location, color, equipment, is_active)
SELECT 'Байконур', 'Средняя переговорная для рабочих групп', 8, 2, 'Блок Б, 2 этаж, каб. 210', '#8B5CF6', ARRAY['tv', 'whiteboard']::TEXT[], TRUE
WHERE NOT EXISTS (SELECT 1 FROM meeting_rooms WHERE name = 'Байконур');

INSERT INTO meeting_rooms (name, description, capacity, floor, location, color, equipment, is_active)
SELECT 'Focus Room 1', 'Малая комната для звонков и фокус-сессий', 4, 2, 'Блок Б, 2 этаж, каб. 215', '#F59E0B', ARRAY['whiteboard']::TEXT[], TRUE
WHERE NOT EXISTS (SELECT 1 FROM meeting_rooms WHERE name = 'Focus Room 1');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS room_bookings;
ALTER TABLE events DROP COLUMN IF EXISTS room_id;
DROP TABLE IF EXISTS meeting_rooms;

-- +goose StatementEnd

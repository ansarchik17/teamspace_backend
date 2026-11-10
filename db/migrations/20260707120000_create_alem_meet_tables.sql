-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS alem_meet_rooms (
    room_id TEXT PRIMARY KEY,
    room_code TEXT NOT NULL UNIQUE,
    livekit_room_name TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    join_url TEXT NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users(id),
    host_user_id BIGINT NOT NULL REFERENCES users(id),
    host_display_name TEXT NOT NULL,
    host_avatar_url TEXT,
    is_locked BOOLEAN NOT NULL DEFAULT false,
    lobby_enabled BOOLEAN NOT NULL DEFAULT true,
    join_requires_host BOOLEAN NOT NULL DEFAULT false,
    mute_on_entry BOOLEAN NOT NULL DEFAULT false,
    allow_screen_share BOOLEAN NOT NULL DEFAULT true,
    allow_chat BOOLEAN NOT NULL DEFAULT true,
    allow_hand_raise BOOLEAN NOT NULL DEFAULT true,
    max_participants INTEGER NOT NULL DEFAULT 100,
    theme_preference TEXT NOT NULL DEFAULT 'system',
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alem_meet_rooms_host_user_id ON alem_meet_rooms(host_user_id);
CREATE INDEX IF NOT EXISTS idx_alem_meet_rooms_created_by ON alem_meet_rooms(created_by);

CREATE TABLE IF NOT EXISTS alem_meet_participants (
    id BIGSERIAL PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES alem_meet_rooms(room_id) ON DELETE CASCADE,
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    identity TEXT NOT NULL,
    display_name TEXT NOT NULL,
    avatar_url TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'guest',
    status TEXT NOT NULL DEFAULT 'invited',
    is_hand_raised BOOLEAN NOT NULL DEFAULT false,
    is_muted BOOLEAN NOT NULL DEFAULT false,
    joined_at TIMESTAMPTZ,
    left_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(room_id, identity),
    UNIQUE(room_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_alem_meet_participants_room_id ON alem_meet_participants(room_id);
CREATE INDEX IF NOT EXISTS idx_alem_meet_participants_user_id ON alem_meet_participants(user_id);

CREATE TABLE IF NOT EXISTS alem_meet_recordings (
    id BIGSERIAL PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES alem_meet_rooms(room_id) ON DELETE CASCADE,
    egress_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'recording',
    download_url TEXT NOT NULL DEFAULT '',
    file_path TEXT NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0,
    started_by BIGINT NOT NULL REFERENCES users(id),
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alem_meet_recordings_room_id ON alem_meet_recordings(room_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS alem_meet_recordings;
DROP TABLE IF EXISTS alem_meet_participants;
DROP TABLE IF EXISTS alem_meet_rooms;

-- +goose StatementEnd

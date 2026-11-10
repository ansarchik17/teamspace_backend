-- +goose Up
CREATE TABLE IF NOT EXISTS call_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id UUID NOT NULL REFERENCES call_history(call_id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL, -- Assuming users table exists, but maybe not foreign keyed strictly to avoid hard dependency here if user is deleted? Ideally should be FK.
    status TEXT NOT NULL DEFAULT 'joined', -- joined, left
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at TIMESTAMPTZ,
    role TEXT DEFAULT 'participant', -- host, participant
    CONSTRAINT uq_call_participant UNIQUE (call_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_call_participants_call_id ON call_participants(call_id);
CREATE INDEX IF NOT EXISTS idx_call_participants_user_id ON call_participants(user_id);

-- +goose Down
DROP TABLE IF EXISTS call_participants;

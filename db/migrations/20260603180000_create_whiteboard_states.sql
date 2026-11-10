-- +goose Up
CREATE TABLE IF NOT EXISTS whiteboard_states (
    board_id UUID PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    state JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    updated_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_whiteboard_states_updated_at
    ON whiteboard_states (updated_at DESC);

-- +goose Down
DROP TABLE IF EXISTS whiteboard_states;

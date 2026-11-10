-- +goose Up
CREATE TABLE note_object_cleanup (
    object_name TEXT PRIMARY KEY,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE note_object_cleanup;

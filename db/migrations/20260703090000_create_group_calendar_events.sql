-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS group_calendar_events (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_group_calendar_events_group_created_at
    ON group_calendar_events (group_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_group_calendar_events_event
    ON group_calendar_events (event_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_group_calendar_events_event;
DROP INDEX IF EXISTS idx_group_calendar_events_group_created_at;
DROP TABLE IF EXISTS group_calendar_events;

-- +goose StatementEnd

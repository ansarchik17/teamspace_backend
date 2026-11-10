-- +goose Up
-- Calendar release migrations follow the deployed main migration sequence.
CREATE TABLE event_exceptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    occurrence_date TIMESTAMPTZ NOT NULL,
    exception_type TEXT NOT NULL CHECK (exception_type IN ('modified', 'deleted')),
    event_data JSONB,
    start_time TIMESTAMPTZ,
    end_time TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT event_exceptions_unique_occurrence UNIQUE (event_id, occurrence_date),
    CONSTRAINT event_exceptions_data_check CHECK (
        (exception_type = 'modified' AND event_data IS NOT NULL AND start_time IS NOT NULL AND end_time > start_time) OR
        (exception_type = 'deleted' AND event_data IS NULL AND start_time IS NULL AND end_time IS NULL)
    )
);
CREATE INDEX event_exceptions_event_idx ON event_exceptions(event_id);
CREATE INDEX event_exceptions_moved_idx ON event_exceptions(start_time, end_time)
    WHERE exception_type = 'modified';

-- +goose Down
DROP TABLE IF EXISTS event_exceptions;

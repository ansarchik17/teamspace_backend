-- +goose Up
-- +goose StatementBegin

-- если нет расширения для gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE event_reminders (
                                 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                 event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
                                 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

                                 minutes_before INT NOT NULL CHECK (minutes_before >= 0),

                                 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- чтобы не слать повторно для одной и той же "occurrence" (важно для recurring и рестартов)
                                 last_sent_occurrence_start TIMESTAMPTZ NULL,
                                 last_sent_at TIMESTAMPTZ NULL
);

-- один reminder на (event_id, user_id) (если хочешь несколько напоминаний на одно событие — убери UNIQUE)
CREATE UNIQUE INDEX uq_event_reminders_event_user ON event_reminders(event_id, user_id);

CREATE INDEX idx_event_reminders_user_id ON event_reminders(user_id);
CREATE INDEX idx_event_reminders_event_id ON event_reminders(event_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_reminders;
-- +goose StatementEnd

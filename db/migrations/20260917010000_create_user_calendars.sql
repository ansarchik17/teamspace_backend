-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_calendars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(120) NOT NULL,
    color VARCHAR(20) NOT NULL DEFAULT '#2161F5',
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_user_calendars_default
    ON user_calendars(user_id) WHERE is_default;
CREATE INDEX idx_user_calendars_user_id ON user_calendars(user_id);

INSERT INTO user_calendars (user_id, name, color, is_default)
SELECT id, 'Мой календарь', '#2161F5', TRUE FROM users;

ALTER TABLE events ADD COLUMN calendar_id UUID REFERENCES user_calendars(id) ON DELETE RESTRICT;

UPDATE events e
SET calendar_id = c.id
FROM user_calendars c
WHERE c.user_id = e.user_id AND c.is_default = TRUE;

ALTER TABLE events ALTER COLUMN calendar_id SET NOT NULL;
CREATE INDEX idx_events_calendar_id ON events(calendar_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events DROP COLUMN IF EXISTS calendar_id;
DROP TABLE IF EXISTS user_calendars;
-- +goose StatementEnd

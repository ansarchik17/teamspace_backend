-- +goose Up
-- Calendar release migrations follow the deployed main migration sequence.
-- +goose StatementBegin

CREATE TABLE user_calendar_settings (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    calendar_id UUID NOT NULL REFERENCES user_calendars(id) ON DELETE CASCADE,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    display_order INT NOT NULL DEFAULT 0 CHECK (display_order BETWEEN 0 AND 10000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, calendar_id)
);

CREATE INDEX idx_user_calendar_settings_user_id ON user_calendar_settings(user_id);
CREATE INDEX idx_user_calendar_settings_calendar_id ON user_calendar_settings(calendar_id);

CREATE TABLE user_group_calendar_settings (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    display_order INT NOT NULL DEFAULT 0 CHECK (display_order BETWEEN 0 AND 10000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX idx_user_group_calendar_settings_user
    ON user_group_calendar_settings(user_id);

INSERT INTO user_calendar_settings (user_id, calendar_id, is_visible, display_order)
SELECT user_id, id, TRUE, 0 FROM user_calendars
ON CONFLICT (user_id, calendar_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_group_calendar_settings;
DROP TABLE IF EXISTS user_calendar_settings;
-- +goose StatementEnd

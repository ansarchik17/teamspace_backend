-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_section_visits (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    section TEXT NOT NULL,
    route_path TEXT,
    visited_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_section_visits_user_id ON user_section_visits(user_id);
CREATE INDEX idx_user_section_visits_section ON user_section_visits(section);
CREATE INDEX idx_user_section_visits_visited_at ON user_section_visits(visited_at DESC);
CREATE INDEX idx_user_section_visits_user_section_visited_at
    ON user_section_visits(user_id, section, visited_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_user_section_visits_user_section_visited_at;
DROP INDEX IF EXISTS idx_user_section_visits_visited_at;
DROP INDEX IF EXISTS idx_user_section_visits_section;
DROP INDEX IF EXISTS idx_user_section_visits_user_id;
DROP TABLE IF EXISTS user_section_visits;
-- +goose StatementEnd

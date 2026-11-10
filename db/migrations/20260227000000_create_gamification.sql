-- +goose Up
-- +goose StatementBegin

-- Track total scores of a user
CREATE TABLE IF NOT EXISTS user_gamification_scores (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    total_score INT DEFAULT 0,
    level INT DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Track specific actions
CREATE TABLE IF NOT EXISTS user_gamification_activities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    action_type VARCHAR(50) NOT NULL,
    points INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_gamification_user_id_created ON user_gamification_activities(user_id, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_gamification_user_id_created;
DROP TABLE IF EXISTS user_gamification_activities;
DROP TABLE IF EXISTS user_gamification_scores;

-- +goose StatementEnd

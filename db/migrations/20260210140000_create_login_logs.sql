-- +goose Up
-- +goose StatementBegin
CREATE TABLE login_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id BIGINT NOT NULL REFERENCES users(id),
    email TEXT,
    login_time TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_login_logs_user_id ON login_logs(user_id);
CREATE INDEX idx_login_logs_login_time ON login_logs(login_time);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_login_logs_login_time;
DROP INDEX IF EXISTS idx_login_logs_user_id;
DROP TABLE IF EXISTS login_logs;
-- +goose StatementEnd

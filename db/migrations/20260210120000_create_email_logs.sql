-- +goose Up
-- +goose StatementBegin
CREATE TABLE email_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sent_email TEXT NOT NULL,
    matched_user_id BIGINT REFERENCES users(id),
    content TEXT,
    status TEXT NOT NULL, -- 'matched', 'unmatched'
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_email_logs_sent_email ON email_logs(sent_email);
CREATE INDEX idx_email_logs_matched_user_id ON email_logs(matched_user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_email_logs_matched_user_id;
DROP INDEX IF EXISTS idx_email_logs_sent_email;
DROP TABLE IF EXISTS email_logs;
-- +goose StatementEnd

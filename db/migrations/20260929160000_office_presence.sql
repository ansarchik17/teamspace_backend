-- +goose Up
CREATE TABLE drive_office_presence (
 session_id UUID PRIMARY KEY,
 file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 document_key TEXT NOT NULL,
 last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX drive_office_presence_file_key ON drive_office_presence(file_id,document_key,last_seen_at);
CREATE INDEX drive_office_presence_expiry ON drive_office_presence(last_seen_at);

-- +goose Down
DROP TABLE drive_office_presence;

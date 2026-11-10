-- +goose Up
CREATE TABLE drive_office_save_failures (
 file_id UUID PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
 document_key TEXT NOT NULL,
 version INTEGER NOT NULL CHECK (version > 0),
 kind TEXT NOT NULL CHECK (kind IN ('save_failed','document_server_error')),
 failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE drive_office_save_failures;

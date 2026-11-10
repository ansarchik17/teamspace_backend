-- +goose Up
CREATE TABLE drive_office_save_receipts (
 file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 document_key TEXT NOT NULL,
 event_hash VARCHAR(64) NOT NULL,
 version INTEGER NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (file_id, document_key, event_hash)
);

-- +goose Down
DROP TABLE drive_office_save_receipts;

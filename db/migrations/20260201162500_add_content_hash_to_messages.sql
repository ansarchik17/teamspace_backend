-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE messages ADD COLUMN content_hash VARCHAR(64);

CREATE INDEX idx_messages_content_hash ON messages(content_hash);

UPDATE messages 
SET content_hash = encode(digest(COALESCE(content, ''), 'sha256'), 'hex')
WHERE content IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_content_hash;
ALTER TABLE messages DROP COLUMN content_hash;
-- +goose StatementEnd

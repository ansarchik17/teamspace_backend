-- +goose Up
-- +goose StatementBegin
ALTER TABLE messages 
    ADD COLUMN is_ephemeral BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN ttl_seconds INTEGER,
    ADD COLUMN expires_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX idx_messages_ephemeral_expires 
ON messages (expires_at) 
WHERE is_ephemeral = true AND expires_at IS NOT NULL AND is_deleted = false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_ephemeral_expires;

ALTER TABLE messages
    DROP COLUMN expires_at,
    DROP COLUMN ttl_seconds,
    DROP COLUMN is_ephemeral;
-- +goose StatementEnd

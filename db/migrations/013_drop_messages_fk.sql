-- +goose Up
-- +goose StatementBegin

-- Drop the foreign key constraint from messages to conversations
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_conversation_id_fkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore the foreign key constraint (Note: this might fail if there are messages referencing groups that are not in conversations table)
-- We only add it back if we are rolling back and know data is consistent, or we accept it might fail.
-- Ideally we would have a way to distinguish group messages from conversation messages if we want strict integrity.
ALTER TABLE messages ADD CONSTRAINT messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;

-- +goose StatementEnd

-- +goose Up
-- Rollback changes: make encrypted_keys nullable again and remove default
DROP INDEX IF EXISTS idx_messages_encrypted_keys;
ALTER TABLE messages ALTER COLUMN encrypted_keys DROP NOT NULL;
ALTER TABLE messages ALTER COLUMN encrypted_keys DROP DEFAULT;

-- +goose Down
-- Re-apply constraints: default '{}', not null, and index
UPDATE messages SET encrypted_keys = '{}' WHERE encrypted_keys IS NULL;
ALTER TABLE messages ALTER COLUMN encrypted_keys SET DEFAULT '{}';
ALTER TABLE messages ALTER COLUMN encrypted_keys SET NOT NULL;
CREATE INDEX idx_messages_encrypted_keys ON messages USING gin (encrypted_keys);

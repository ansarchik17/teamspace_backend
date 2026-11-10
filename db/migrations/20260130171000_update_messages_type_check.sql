-- +goose Up
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_type_check CHECK (type IN ('text', 'image', 'video', 'file', 'audio', 'sticker', 'share_task', 'share_event'));

-- +goose Down
-- Warning: Down migration might fail if there are rows with new types
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_type_check CHECK (type IN ('text', 'image', 'video', 'file', 'audio', 'sticker'));

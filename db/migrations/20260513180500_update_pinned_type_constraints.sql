-- +goose Up
-- Update pinned_messages chat_type constraint
ALTER TABLE pinned_messages DROP CONSTRAINT IF EXISTS pinned_messages_chat_type_check;
ALTER TABLE pinned_messages ADD CONSTRAINT pinned_messages_chat_type_check CHECK (chat_type IN ('personal', 'group', 'channel'));

-- Update pinned_chats chat_type constraint
ALTER TABLE pinned_chats DROP CONSTRAINT IF EXISTS pinned_chats_chat_type_check;
ALTER TABLE pinned_chats ADD CONSTRAINT pinned_chats_chat_type_check CHECK (chat_type IN ('personal', 'group', 'channel', 'ai', 'support'));

-- +goose Down
-- Revert pinned_messages chat_type constraint
ALTER TABLE pinned_messages DROP CONSTRAINT IF EXISTS pinned_messages_chat_type_check;
ALTER TABLE pinned_messages ADD CONSTRAINT pinned_messages_chat_type_check CHECK (chat_type IN ('personal', 'group'));

-- Revert pinned_chats chat_type constraint
ALTER TABLE pinned_chats DROP CONSTRAINT IF EXISTS pinned_chats_chat_type_check;
ALTER TABLE pinned_chats ADD CONSTRAINT pinned_chats_chat_type_check CHECK (chat_type IN ('personal', 'group'));

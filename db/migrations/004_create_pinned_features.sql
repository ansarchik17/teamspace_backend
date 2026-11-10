-- +goose Up
-- Create pinned_chats table
CREATE TABLE IF NOT EXISTS pinned_chats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id UUID NOT NULL,
    chat_type VARCHAR(20) NOT NULL CHECK (chat_type IN ('personal', 'group')),
    position INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, chat_id, chat_type)
);

-- Create index for faster lookups
CREATE INDEX idx_pinned_chats_user_id ON pinned_chats(user_id);
CREATE INDEX idx_pinned_chats_position ON pinned_chats(user_id, position);

-- Create pinned_messages table
CREATE TABLE IF NOT EXISTS pinned_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id UUID NOT NULL,
    chat_type VARCHAR(20) NOT NULL CHECK (chat_type IN ('personal', 'group')),
    message_id UUID NOT NULL,
    pinned_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    note TEXT,
    position INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(message_id)
);

-- Create indexes for faster lookups
CREATE INDEX idx_pinned_messages_chat ON pinned_messages(chat_id, chat_type);
CREATE INDEX idx_pinned_messages_position ON pinned_messages(chat_id, chat_type, position);

-- +goose Down
DROP TABLE IF EXISTS pinned_messages;
DROP TABLE IF EXISTS pinned_chats;

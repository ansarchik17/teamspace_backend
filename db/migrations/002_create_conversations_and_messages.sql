-- +goose Up
-- +goose StatementBegin

-- Conversations table (personal and group chats)
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(20) NOT NULL CHECK (type IN ('personal', 'group')),
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

-- Conversation participants
CREATE TABLE conversation_participants (
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at TIMESTAMPTZ,
    last_read_at TIMESTAMPTZ,
    is_muted BOOLEAN NOT NULL DEFAULT false,
    is_blocked BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id)
);

-- Messages table
CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reply_to_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    content TEXT,
    type VARCHAR(20) NOT NULL DEFAULT 'text' CHECK (type IN ('text', 'image', 'video', 'file', 'audio', 'sticker')),
    attachment_url TEXT,
    attachment_type VARCHAR(50),
    attachment_size BIGINT,
    metadata JSONB, -- For additional data like image dimensions, duration, etc.
    is_edited BOOLEAN NOT NULL DEFAULT false,
    is_deleted BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    edited_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);

-- Message status (delivery and read receipts)
CREATE TABLE message_status (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delivered_at TIMESTAMPTZ,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(message_id, user_id)
);

-- Indexes for conversations
CREATE INDEX idx_conversations_type ON conversations(type) WHERE deleted_at IS NULL;
CREATE INDEX idx_conversations_created_by ON conversations(created_by);

-- Indexes for participants
CREATE INDEX idx_participants_conversation ON conversation_participants(conversation_id) WHERE left_at IS NULL;
CREATE INDEX idx_participants_user ON conversation_participants(user_id) WHERE left_at IS NULL;
CREATE INDEX idx_participants_active ON conversation_participants(conversation_id, user_id) WHERE left_at IS NULL;

-- Indexes for messages
CREATE INDEX idx_messages_conversation ON messages(conversation_id, created_at DESC, id) WHERE is_deleted = false;
CREATE INDEX idx_messages_sender ON messages(sender_id);
CREATE INDEX idx_messages_reply_to ON messages(reply_to_id) WHERE reply_to_id IS NOT NULL;
CREATE INDEX idx_messages_type ON messages(type);
CREATE INDEX idx_messages_search ON messages USING gin(to_tsvector('english', content)) WHERE content IS NOT NULL;

-- Indexes for message status
CREATE INDEX idx_message_status_message ON message_status(message_id);
CREATE INDEX idx_message_status_user ON message_status(user_id);
CREATE INDEX idx_message_status_unread ON message_status(user_id, message_id) WHERE read_at IS NULL;
CREATE INDEX idx_message_status_undelivered ON message_status(user_id, message_id) WHERE delivered_at IS NULL;

-- Comments
COMMENT ON TABLE conversations IS 'Chat conversations (personal and group)';
COMMENT ON TABLE conversation_participants IS 'Participants in conversations';
COMMENT ON TABLE messages IS 'Chat messages';
COMMENT ON TABLE message_status IS 'Message delivery and read status';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_message_status_undelivered;
DROP INDEX IF EXISTS idx_message_status_unread;
DROP INDEX IF EXISTS idx_message_status_user;
DROP INDEX IF EXISTS idx_message_status_message;
DROP INDEX IF EXISTS idx_messages_search;
DROP INDEX IF EXISTS idx_messages_type;
DROP INDEX IF EXISTS idx_messages_reply_to;
DROP INDEX IF EXISTS idx_messages_sender;
DROP INDEX IF EXISTS idx_messages_conversation;
DROP INDEX IF EXISTS idx_participants_active;
DROP INDEX IF EXISTS idx_participants_user;
DROP INDEX IF EXISTS idx_participants_conversation;
DROP INDEX IF EXISTS idx_conversations_created_by;
DROP INDEX IF EXISTS idx_conversations_type;

DROP TABLE IF EXISTS message_status;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversation_participants;
DROP TABLE IF EXISTS conversations;

-- +goose StatementEnd

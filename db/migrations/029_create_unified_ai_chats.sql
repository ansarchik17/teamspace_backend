-- +goose Up
-- Create table for AI Unified Chats (one AI chat per user - WhatsApp style)
CREATE TABLE IF NOT EXISTS unified_ai_chats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE, -- UNIQUE constraint ensures singleton per user
    title TEXT NOT NULL DEFAULT 'AI Assistant',
    model_name TEXT NOT NULL DEFAULT 'openai/gpt-oss-120b',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- AI Unified Messages (History)
CREATE TABLE IF NOT EXISTS unified_ai_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id UUID NOT NULL REFERENCES unified_ai_chats(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL, -- 'user' or 'assistant'
    content TEXT NOT NULL,
    file_url TEXT, -- URL to MinIO if file attached
    tokens_usage INT DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_unified_ai_chats_user ON unified_ai_chats(user_id);
CREATE INDEX IF NOT EXISTS idx_unified_ai_messages_chat ON unified_ai_messages(chat_id);

-- +goose Down
DROP TABLE IF EXISTS unified_ai_messages;
DROP TABLE IF EXISTS unified_ai_chats;

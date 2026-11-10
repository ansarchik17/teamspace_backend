-- +goose Up
CREATE TABLE notebook_chats (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE notebook_messages (
    id UUID PRIMARY KEY,
    chat_id UUID NOT NULL REFERENCES notebook_chats(id) ON DELETE CASCADE,
    role TEXT NOT NULL, -- 'user' or 'assistant'
    type TEXT NOT NULL DEFAULT 'text', -- 'text', 'cards', 'test', 'table', 'report'
    content TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_notebook_chats_user ON notebook_chats(user_id);
CREATE INDEX idx_notebook_messages_chat ON notebook_messages(chat_id);

-- +goose Down
DROP TABLE IF EXISTS notebook_messages;
DROP TABLE IF EXISTS notebook_chats;

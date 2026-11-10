-- +goose Up
-- +goose StatementBegin
-- 1. Индекс для БЫСТРОЙ загрузки истории чата.
CREATE INDEX IF NOT EXISTS idx_messages_conversation_history 
ON messages (conversation_id, created_at DESC) 
WHERE is_deleted = false;

-- 2. Индекс для списка чатов пользователя.
CREATE INDEX IF NOT EXISTS idx_participants_user_chats 
ON conversation_participants (user_id, conversation_id) 
WHERE left_at IS NULL AND hidden_at IS NULL;

-- 3. Индекс для статусов сообщений.
CREATE INDEX IF NOT EXISTS idx_message_status_lookup 
ON message_status (message_id, user_id);

-- 4. Индекс для поиска пользователей (поиск по имени/email).
CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);
CREATE INDEX IF NOT EXISTS idx_users_username ON users (username);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_conversation_history;
DROP INDEX IF EXISTS idx_participants_user_chats;
DROP INDEX IF EXISTS idx_message_status_lookup;
DROP INDEX IF EXISTS idx_users_email;
DROP INDEX IF EXISTS idx_users_username;
-- +goose StatementEnd

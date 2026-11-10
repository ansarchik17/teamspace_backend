-- +goose Up
-- последнее сообщение в диалоге
CREATE INDEX IF NOT EXISTS ix_messages_conv_created_desc
    ON messages (conversation_id, created_at DESC)
    WHERE is_deleted = false;

-- unread: быстрый поиск статуса чтения
CREATE INDEX IF NOT EXISTS ix_message_status_user_message
    ON message_status (user_id, message_id);

-- полезно для фильтров read_at IS NULL / IS NOT NULL
CREATE INDEX IF NOT EXISTS ix_message_status_user_readat
    ON message_status (user_id, read_at);

-- участники: фильтры left_at + связи
CREATE INDEX IF NOT EXISTS ix_cp_user_left
    ON conversation_participants (user_id, left_at, conversation_id);

CREATE INDEX IF NOT EXISTS ix_cp_conv_user
    ON conversation_participants (conversation_id, user_id);

-- +goose Down
DROP INDEX IF EXISTS ix_cp_conv_user;
DROP INDEX IF EXISTS ix_cp_user_left;
DROP INDEX IF EXISTS ix_message_status_user_readat;
DROP INDEX IF EXISTS ix_message_status_user_message;
DROP INDEX IF EXISTS ix_messages_conv_created_desc;
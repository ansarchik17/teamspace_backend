-- +goose Up
-- Добавляем новые типы сообщений: voice (голосовое сообщение) и video_message (видеосообщение)
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_type_check CHECK (type IN ('text', 'image', 'video', 'file', 'audio', 'sticker', 'share_task', 'share_event', 'voice', 'video_message'));

-- +goose Down
-- Возврат к предыдущим типам
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_type_check CHECK (type IN ('text', 'image', 'video', 'file', 'audio', 'sticker', 'share_task', 'share_event'));

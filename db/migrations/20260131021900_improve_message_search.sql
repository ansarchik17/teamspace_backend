-- +goose Up
-- +goose StatementBegin
-- ========================================
-- Full-Text Search для сообщений
-- Улучшение поиска как в Telegram/WhatsApp
-- ========================================

-- 1. Добавляем tsvector колонку для полнотекстового поиска
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'messages' 
        AND column_name = 'search_vector'
    ) THEN
        ALTER TABLE messages ADD COLUMN search_vector tsvector;
    END IF;
END $$;

-- 2. Создаём или обновляем функцию для обновления search_vector
CREATE OR REPLACE FUNCTION messages_search_vector_update() 
RETURNS trigger AS $$
BEGIN
    NEW.search_vector := setweight(
        to_tsvector('russian', COALESCE(NEW.content, '')), 
        'A'
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 3. Создаём триггер для автоматического обновления
DROP TRIGGER IF EXISTS messages_search_vector_trigger ON messages;

CREATE TRIGGER messages_search_vector_trigger
    BEFORE INSERT OR UPDATE OF content
    ON messages
    FOR EACH ROW
    EXECUTE FUNCTION messages_search_vector_update();

-- 4. Заполняем search_vector для ВСЕХ существующих сообщений
DO $$
BEGIN
    UPDATE messages 
    SET search_vector = setweight(
        to_tsvector('russian', COALESCE(content, '')), 
        'A'
    )
    WHERE content IS NOT NULL;
END $$;

-- 5. Создаём GIN индекс для быстрого полнотекстового поиска
DROP INDEX IF EXISTS idx_messages_search_vector;
CREATE INDEX idx_messages_search_vector ON messages USING GIN(search_vector);

-- 6. Создаём дополнительные индексы для оптимизации
DROP INDEX IF EXISTS idx_messages_conversation_created;
CREATE INDEX idx_messages_conversation_created ON messages(conversation_id, created_at DESC) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_messages_sender_created;
CREATE INDEX idx_messages_sender_created ON messages(sender_id, created_at DESC) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_messages_conversation_search;
CREATE INDEX idx_messages_conversation_search ON messages(conversation_id, created_at DESC) WHERE deleted_at IS NULL AND search_vector IS NOT NULL;

-- 7. Комментарии и анализ
COMMENT ON COLUMN messages.search_vector IS 'Full-text search vector for message content';
ANALYZE messages;

-- Исправлено: RAISE завернут в DO блок
DO $$
BEGIN
    RAISE NOTICE '✅ Full-text search migration completed successfully!';
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 1. Удаляем индексы
DROP INDEX IF EXISTS idx_messages_conversation_search;
DROP INDEX IF EXISTS idx_messages_sender_created;
DROP INDEX IF EXISTS idx_messages_conversation_created;
DROP INDEX IF EXISTS idx_messages_search_vector;

-- 2. Удаляем триггер и функцию
DROP TRIGGER IF EXISTS messages_search_vector_trigger ON messages;
DROP FUNCTION IF EXISTS messages_search_vector_update();

-- 3. Удаляем колонку
DO $$ 
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'messages' 
        AND column_name = 'search_vector'
    ) THEN
        ALTER TABLE messages DROP COLUMN search_vector;
    END IF;
END $$;

-- Исправлено: RAISE завернут в DO блок
DO $$
BEGIN
    RAISE NOTICE '✅ Full-text search migration rolled back successfully!';
END $$;
-- +goose StatementEnd

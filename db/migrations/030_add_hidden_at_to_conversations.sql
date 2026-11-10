-- +goose Up
-- +goose StatementBegin

-- Добавляем поле hidden_at для скрытия conversation конкретным пользователем
ALTER TABLE conversation_participants 
ADD COLUMN hidden_at TIMESTAMPTZ;

-- Добавляем поле last_cleared_at для запоминания момента когда пользователь "очистил" историю
-- При восстановлении чата показываем только сообщения ПОСЛЕ этого времени
ALTER TABLE conversation_participants 
ADD COLUMN last_cleared_at TIMESTAMPTZ;

-- Создаем индекс для быстрой фильтрации скрытых чатов
CREATE INDEX idx_conversation_participants_hidden 
ON conversation_participants(user_id, hidden_at) 
WHERE hidden_at IS NULL;

COMMENT ON COLUMN conversation_participants.hidden_at IS 'Timestamp когда пользователь скрыл этот conversation у себя (NULL = виден)';
COMMENT ON COLUMN conversation_participants.last_cleared_at IS 'Timestamp последнего скрытия - сообщения до этого момента не показываются';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_conversation_participants_hidden;
ALTER TABLE conversation_participants DROP COLUMN IF EXISTS last_cleared_at;
ALTER TABLE conversation_participants DROP COLUMN IF EXISTS hidden_at;

-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

-- Добавляем поле hidden_at для скрытия группы конкретным пользователем
ALTER TABLE group_members 
ADD COLUMN hidden_at TIMESTAMPTZ;

-- Добавляем поле last_cleared_at для запоминания момента когда пользователь "очистил" историю
-- При восстановлении группы показываем только сообщения ПОСЛЕ этого времени
ALTER TABLE group_members 
ADD COLUMN last_cleared_at TIMESTAMPTZ;

-- Создаем индекс для быстрой фильтрации скрытых групп
CREATE INDEX idx_group_members_hidden 
ON group_members(user_id, hidden_at) 
WHERE hidden_at IS NULL;

COMMENT ON COLUMN group_members.hidden_at IS 'Timestamp когда пользователь скрыл эту группу у себя (NULL = виден)';
COMMENT ON COLUMN group_members.last_cleared_at IS 'Timestamp последнего скрытия - сообщения до этого момента не показываются';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_group_members_hidden;
ALTER TABLE group_members DROP COLUMN IF EXISTS last_cleared_at;
ALTER TABLE group_members DROP COLUMN IF EXISTS hidden_at;

-- +goose StatementEnd

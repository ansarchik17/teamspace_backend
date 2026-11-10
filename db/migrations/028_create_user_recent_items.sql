-- +goose Up
-- +goose StatementBegin

-- 1. Создаем таблицу для истории просмотров
CREATE TABLE IF NOT EXISTS user_recent_items (
                                                 user_id BIGINT NOT NULL,
                                                 resource_type VARCHAR(20) NOT NULL CHECK (resource_type IN ('file', 'folder')),
                                                 resource_id UUID NOT NULL,
                                                 last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                                                 PRIMARY KEY (user_id, resource_type, resource_id)
);

-- Индекс для быстрой выборки "Топ-20 последних"
CREATE INDEX IF NOT EXISTS idx_user_recent_accessed ON user_recent_items(user_id, last_accessed_at DESC);

-- 2. Добавляем колонку onlyoffice_key, если её нет (для интеграции редактора)
ALTER TABLE files ADD COLUMN IF NOT EXISTS onlyoffice_key VARCHAR(255);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_recent_items;
ALTER TABLE files DROP COLUMN IF EXISTS onlyoffice_key;
-- +goose StatementEnd
-- +goose Up
-- Таблица для хранения истории изменений задач (как в Jira)
CREATE TABLE IF NOT EXISTS task_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(50) NOT NULL, -- created, updated, moved, assigned, unassigned, deleted, etc.
    field_name VARCHAR(100), -- Какое поле было изменено (title, description, priority, status, assignee, etc.)
    old_value TEXT, -- Старое значение (JSON или текст)
    new_value TEXT, -- Новое значение (JSON или текст)
    metadata JSONB, -- Дополнительная информация (например, from_column, to_column для move)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Индексы для быстрого поиска истории
CREATE INDEX IF NOT EXISTS idx_task_history_task_id ON task_history(task_id);
CREATE INDEX IF NOT EXISTS idx_task_history_created_at ON task_history(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_history_task_created ON task_history(task_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS task_history;

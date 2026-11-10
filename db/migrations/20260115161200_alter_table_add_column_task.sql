-- +goose Up
-- +goose StatementBegin

-- 1) user_id -> assignee_id (владелец доски / кому назначена задача)
ALTER TABLE tasks RENAME COLUMN user_id TO assignee_id;

-- 2) created_by добавляем пока NULL, чтобы не сломать существующие строки
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS created_by BIGINT;

-- 3) заполняем created_by для старых задач
--    Вариант A (самый безопасный): считаем что создатель = assignee
UPDATE tasks
SET created_by = assignee_id
WHERE created_by IS NULL;

-- 4) теперь можно делать NOT NULL
ALTER TABLE tasks
    ALTER COLUMN created_by SET NOT NULL;

-- 5) индексы
CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee_id);
CREATE INDEX IF NOT EXISTS idx_tasks_created_by ON tasks(created_by);

-- 6) FK добавляем только после того как значения валидные
ALTER TABLE tasks
    ADD CONSTRAINT fk_tasks_assignee
        FOREIGN KEY (assignee_id) REFERENCES users(id);

ALTER TABLE tasks
    ADD CONSTRAINT fk_tasks_created_by
        FOREIGN KEY (created_by) REFERENCES users(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS fk_tasks_created_by;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS fk_tasks_assignee;

DROP INDEX IF EXISTS idx_tasks_created_by;
DROP INDEX IF EXISTS idx_tasks_assignee;

ALTER TABLE tasks DROP COLUMN IF EXISTS created_by;

ALTER TABLE tasks RENAME COLUMN assignee_id TO user_id;

-- +goose StatementEnd
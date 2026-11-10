-- +goose Up

-- 1) колонка
ALTER TABLE task_assignees
    ADD COLUMN IF NOT EXISTS column_id uuid;

-- 2) Backfill
UPDATE task_assignees ta
SET column_id = t.column_id
FROM tasks t
WHERE ta.task_id = t.id
  AND ta.column_id IS NULL
  AND t.column_id IS NOT NULL;

-- 3) чистка дублей (task_id,user_id)
DELETE FROM task_assignees a
    USING task_assignees b
WHERE a.ctid < b.ctid
  AND a.task_id = b.task_id
  AND a.user_id = b.user_id;

-- 4) FK (идемпотентно без DO)
ALTER TABLE task_assignees
    DROP CONSTRAINT IF EXISTS fk_task_assignees_column;

ALTER TABLE task_assignees
    ADD CONSTRAINT fk_task_assignees_column
        FOREIGN KEY (column_id) REFERENCES kanban_columns(id)
            ON DELETE SET NULL;

-- 5) индексы
CREATE INDEX IF NOT EXISTS idx_task_assignees_user_column
    ON task_assignees(user_id, column_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_task_assignees_task_user
    ON task_assignees(task_id, user_id);

-- +goose Down

ALTER TABLE task_assignees DROP CONSTRAINT IF EXISTS fk_task_assignees_column;

DROP INDEX IF EXISTS idx_task_assignees_user_column;
DROP INDEX IF EXISTS uq_task_assignees_task_user;

ALTER TABLE task_assignees DROP COLUMN IF EXISTS column_id;
-- +goose Up
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'К ВЫПОЛНЕНИЮ';
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_status;
ALTER TABLE tasks DROP COLUMN IF EXISTS status;

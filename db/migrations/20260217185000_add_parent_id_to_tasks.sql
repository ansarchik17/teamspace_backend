-- +goose Up
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS parent_id UUID REFERENCES tasks(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_tasks_parent_id ON tasks(parent_id);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_parent_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS parent_id;

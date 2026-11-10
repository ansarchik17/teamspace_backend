-- +goose Up
ALTER TABLE files ADD COLUMN scope text NOT NULL DEFAULT 'drive';
UPDATE files SET scope = 'kanban' WHERE object_name LIKE 'uploads/%';
CREATE INDEX idx_files_scope ON files(scope) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_files_scope;
ALTER TABLE files DROP COLUMN IF EXISTS scope;

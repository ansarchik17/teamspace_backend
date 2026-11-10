-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS tasks_search_title_trgm ON tasks USING gin (title gin_trgm_ops) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS tasks_search_description_trgm ON tasks USING gin (description gin_trgm_ops) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS tasks_search_key_trgm ON tasks USING gin (task_key gin_trgm_ops) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS tasks_search_status_trgm ON tasks USING gin (status gin_trgm_ops) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS tasks_search_board_author ON tasks (board_id, created_by, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS tasks_search_board_order ON tasks (board_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS tasks_search_title_trgm;
DROP INDEX IF EXISTS tasks_search_description_trgm;
DROP INDEX IF EXISTS tasks_search_key_trgm;
DROP INDEX IF EXISTS tasks_search_status_trgm;
DROP INDEX IF EXISTS tasks_search_board_author;
DROP INDEX IF EXISTS tasks_search_board_order;

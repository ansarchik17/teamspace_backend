-- +goose Up
-- +goose NO TRANSACTION

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_board_id_active
    ON tasks(board_id)
    WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_board_id_status_active
    ON tasks(board_id, status)
    WHERE deleted_at IS NULL;

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_board_id_status_active;
DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_board_id_active;

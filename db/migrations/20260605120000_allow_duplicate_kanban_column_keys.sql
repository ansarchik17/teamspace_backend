-- +goose Up
DROP INDEX IF EXISTS ux_kanban_columns_board_key;
DROP INDEX IF EXISTS ux_kanban_columns_user_key;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM kanban_columns
        GROUP BY board_id, key
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore unique index ux_kanban_columns_board_key: duplicate (board_id, key) values exist';
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS ux_kanban_columns_board_key
    ON kanban_columns(board_id, key);

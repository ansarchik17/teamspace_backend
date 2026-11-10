-- +goose Up

-- 1) добавляем board_id
ALTER TABLE kanban_columns
    ADD COLUMN IF NOT EXISTS board_id UUID;

-- 2) FK (ОБЯЗАТЕЛЬНО Обернуть в StatementBegin/End)
-- +goose StatementBegin
DO $$
    BEGIN
        -- Пытаемся добавить ограничение, если оно уже есть - игнорируем ошибку
        BEGIN
            ALTER TABLE kanban_columns
                ADD CONSTRAINT fk_kanban_columns_board
                    FOREIGN KEY (board_id) REFERENCES boards(id) ON DELETE CASCADE;
        EXCEPTION
            WHEN duplicate_object THEN NULL;
        END;
    END $$;
-- +goose StatementEnd

-- 3) удаляем старую уникальность
ALTER TABLE kanban_columns DROP CONSTRAINT IF EXISTS ux_kanban_columns_user_key;
DROP INDEX IF EXISTS ux_kanban_columns_user_key;

-- 4) обычные индексы
CREATE INDEX IF NOT EXISTS idx_kanban_columns_board_id
    ON kanban_columns(board_id);

-- ВАЖНО: уникальность теперь по доске
CREATE UNIQUE INDEX IF NOT EXISTS ux_kanban_columns_board_key
    ON kanban_columns(board_id, key);
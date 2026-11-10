-- +goose Up
CREATE TABLE IF NOT EXISTS kanban_columns (
                                              id UUID PRIMARY KEY,
                                              user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key VARCHAR(50) NOT NULL,             -- TODO / IN_PROGRESS / TEST / DONE / BLOCK / custom
    name TEXT NOT NULL,                   -- отображаемое имя
    position INT NOT NULL DEFAULT 0,       -- порядок колонок слева-направо
    is_system BOOLEAN NOT NULL DEFAULT FALSE, -- дефолтные колонки нельзя удалить
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
                                                                                               );

CREATE UNIQUE INDEX IF NOT EXISTS ux_kanban_columns_user_key
    ON kanban_columns(user_id, key);

CREATE INDEX IF NOT EXISTS idx_kanban_columns_user_pos
    ON kanban_columns(user_id, position);

-- allowed transitions: из какой колонки в какую можно переносить
CREATE TABLE IF NOT EXISTS kanban_transitions (
                                                  id UUID PRIMARY KEY,
                                                  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    from_column_id UUID NOT NULL REFERENCES kanban_columns(id) ON DELETE CASCADE,
    to_column_id UUID NOT NULL REFERENCES kanban_columns(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
                                                                                                   );

CREATE UNIQUE INDEX IF NOT EXISTS ux_kanban_transitions_unique
    ON kanban_transitions(from_column_id, to_column_id);

CREATE INDEX IF NOT EXISTS idx_kanban_transitions_user
    ON kanban_transitions(user_id);

-- +goose Down
DROP TABLE IF EXISTS kanban_transitions;
DROP TABLE IF EXISTS kanban_columns;
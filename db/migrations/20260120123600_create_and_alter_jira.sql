-- +goose Up
-- +goose NO TRANSACTION

CREATE TABLE IF NOT EXISTS boards (
                                      id UUID PRIMARY KEY,
                                      owner_id BIGINT NOT NULL,
                                      name VARCHAR(255) NOT NULL,
                                      created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
                                      updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
                                      deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS board_members (
                                             board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
                                             user_id BIGINT NOT NULL,
                                             role VARCHAR(50) NOT NULL DEFAULT 'member',
                                             joined_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
                                             PRIMARY KEY (board_id, user_id)
);

CREATE TABLE IF NOT EXISTS sprints (
                                       id UUID PRIMARY KEY,
                                       board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
                                       name VARCHAR(255) NOT NULL,
                                       status VARCHAR(50) NOT NULL DEFAULT 'planned',
                                       start_date TIMESTAMP WITH TIME ZONE,
                                       end_date TIMESTAMP WITH TIME ZONE,
                                       created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
                                       updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
                                       deleted_at TIMESTAMP WITH TIME ZONE
);

ALTER TABLE kanban_columns
    ADD COLUMN IF NOT EXISTS board_id UUID REFERENCES boards(id) ON DELETE CASCADE;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_kanban_columns_board_id ON kanban_columns(board_id);

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS board_id UUID REFERENCES boards(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS sprint_id UUID REFERENCES sprints(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS task_assignees (
                                              task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
                                              user_id BIGINT NOT NULL,
                                              assigned_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
                                              PRIMARY KEY (task_id, user_id)
);

CREATE TABLE IF NOT EXISTS task_attachments (
                                                id UUID PRIMARY KEY,
                                                task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
                                                file_id UUID NOT NULL,
                                                uploaded_by BIGINT NOT NULL,
                                                created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_boards_owner_id ON boards(owner_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_board_sprint ON tasks(board_id, sprint_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sprints_board_id ON sprints(board_id);

-- +goose Down
DROP TABLE IF EXISTS task_attachments;
DROP TABLE IF EXISTS task_assignees;
DROP TABLE IF EXISTS sprints;
DROP TABLE IF EXISTS board_members;
DROP TABLE IF EXISTS boards;
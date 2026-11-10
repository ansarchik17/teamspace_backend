-- +goose Up
CREATE TABLE IF NOT EXISTS tasks (
                                     id UUID PRIMARY KEY,
                                     user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- владелец доски/таска
    column_id UUID NOT NULL REFERENCES kanban_columns(id),
    title TEXT NOT NULL,
    description TEXT,
    priority VARCHAR(20) NOT NULL DEFAULT 'medium', -- low/medium/high/critical
    due_at TIMESTAMP WITH TIME ZONE,
    original_estimate_sec INT NOT NULL DEFAULT 0,
    time_spent_sec INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
                                                                                      );

CREATE INDEX IF NOT EXISTS idx_tasks_user
    ON tasks(user_id) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_user_column
    ON tasks(user_id, column_id) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_due
    ON tasks(user_id, due_at) WHERE deleted_at IS NULL;

-- attachments
CREATE TABLE IF NOT EXISTS task_attachments (
                                                id UUID PRIMARY KEY,
                                                task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    uploaded_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    file_name TEXT,
    mime_type TEXT,
    size_bytes BIGINT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
                                                                                               );

CREATE INDEX IF NOT EXISTS idx_task_attachments_task
    ON task_attachments(task_id);

-- worklogs
CREATE TABLE IF NOT EXISTS task_worklogs (
                                             id UUID PRIMARY KEY,
                                             task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    time_spent_sec INT NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE,
    comment TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
                                                                                            );

CREATE INDEX IF NOT EXISTS idx_task_worklogs_task
    ON task_worklogs(task_id);

-- +goose Down
DROP TABLE IF EXISTS task_worklogs;
DROP TABLE IF EXISTS task_attachments;
DROP TABLE IF EXISTS tasks;
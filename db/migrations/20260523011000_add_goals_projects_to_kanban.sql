-- +goose Up
-- +goose NO TRANSACTION

CREATE TABLE IF NOT EXISTS goals (
    id UUID PRIMARY KEY,
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    created_by BIGINT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY,
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    goal_id UUID REFERENCES goals(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_by BIGINT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS goal_id UUID REFERENCES goals(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_goals_board_id ON goals(board_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_projects_board_id ON projects(board_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_projects_goal_id ON projects(goal_id) WHERE deleted_at IS NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_board_goal ON tasks(board_id, goal_id) WHERE deleted_at IS NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_board_project ON tasks(board_id, project_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_board_project;
DROP INDEX IF EXISTS idx_tasks_board_goal;
DROP INDEX IF EXISTS idx_projects_goal_id;
DROP INDEX IF EXISTS idx_projects_board_id;
DROP INDEX IF EXISTS idx_goals_board_id;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS project_id,
    DROP COLUMN IF EXISTS goal_id;

DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS goals;

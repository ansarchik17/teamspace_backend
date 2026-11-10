-- +goose Up
-- +goose NO TRANSACTION

ALTER TABLE sprints
    ADD COLUMN IF NOT EXISTS goal_id UUID REFERENCES goals(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sprints_goal_id ON sprints(goal_id) WHERE deleted_at IS NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sprints_project_id ON sprints(project_id) WHERE deleted_at IS NULL;

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX IF EXISTS idx_sprints_project_id;
DROP INDEX IF EXISTS idx_sprints_goal_id;

ALTER TABLE sprints
    DROP COLUMN IF EXISTS project_id,
    DROP COLUMN IF EXISTS goal_id;

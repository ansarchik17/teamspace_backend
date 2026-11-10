-- +goose Up
CREATE TABLE IF NOT EXISTS task_assignees (
                                              task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
                                              user_id BIGINT NOT NULL,
                                              created_at TIMESTAMP NOT NULL DEFAULT NOW(),
                                              PRIMARY KEY (task_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_task_assignees_user_id ON task_assignees(user_id);

-- backfill: если раньше assignee_id был один, переносим его как исполнителя
INSERT INTO task_assignees(task_id, user_id)
SELECT id, assignee_id
FROM tasks
WHERE assignee_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- +goose Down
DROP INDEX IF EXISTS idx_task_assignees_user_id;
DROP TABLE IF EXISTS task_assignees;
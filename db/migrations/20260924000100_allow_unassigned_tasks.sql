-- +goose Up
-- Card assignees are optional; task_assignees holds the complete list.
ALTER TABLE tasks ALTER COLUMN assignee_id DROP NOT NULL;

-- +goose Down
-- Refuse rollback while unassigned cards exist, rather than inventing an assignee.
ALTER TABLE tasks ALTER COLUMN assignee_id SET NOT NULL;

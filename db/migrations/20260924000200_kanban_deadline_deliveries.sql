-- +goose Up
CREATE TABLE kanban_deadline_deliveries (
 task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 due_at TIMESTAMPTZ NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('due_soon','overdue')),
 token UUID NOT NULL,
 lease_until TIMESTAMPTZ NOT NULL,
 sent_at TIMESTAMPTZ,
 PRIMARY KEY(task_id,user_id,due_at,kind)
);
CREATE INDEX idx_kanban_tasks_deadline ON tasks(due_at,id) WHERE deleted_at IS NULL AND due_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_kanban_tasks_deadline;
DROP TABLE kanban_deadline_deliveries;

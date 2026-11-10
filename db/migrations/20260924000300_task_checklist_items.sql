-- +goose Up
CREATE TABLE task_checklist_items (
 id UUID PRIMARY KEY,
 task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 title TEXT NOT NULL CHECK (length(btrim(title)) > 0 AND char_length(title) <= 500),
 is_completed BOOLEAN NOT NULL DEFAULT FALSE,
 position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
 created_by BIGINT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_task_checklist_order ON task_checklist_items(task_id,position,created_at,id);

-- +goose Down
DROP TABLE task_checklist_items;

-- +goose Up
CREATE TABLE kanban_labels (
    id UUID PRIMARY KEY,
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name VARCHAR(50) NOT NULL,
    color VARCHAR(20) NOT NULL DEFAULT '#000000',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(board_id, name)
);

CREATE TABLE kanban_task_labels (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES kanban_labels(id) ON DELETE CASCADE,
    assigned_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (task_id, label_id)
);

CREATE INDEX idx_kanban_labels_board_id ON kanban_labels(board_id);
CREATE INDEX idx_kanban_task_labels_task_id ON kanban_task_labels(task_id);
CREATE INDEX idx_kanban_task_labels_label_id ON kanban_task_labels(label_id);

-- +goose Down
DROP TABLE IF EXISTS kanban_task_labels;
DROP TABLE IF EXISTS kanban_labels;

-- +goose Up
ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS source_system VARCHAR(100),
    ADD COLUMN IF NOT EXISTS source_assignment_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS source_document_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS source_url TEXT,
    ADD COLUMN IF NOT EXISTS source_author_sub VARCHAR(255),
    ADD COLUMN IF NOT EXISTS source_department_id VARCHAR(255);

CREATE UNIQUE INDEX IF NOT EXISTS ux_tasks_documentolog_assignment
    ON tasks (source_assignment_id)
    WHERE deleted_at IS NULL
      AND source_system = 'documentolog'
      AND source_assignment_id IS NOT NULL
      AND source_assignment_id <> '';

-- +goose Down
DROP INDEX IF EXISTS ux_tasks_documentolog_assignment;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS source_department_id,
    DROP COLUMN IF EXISTS source_author_sub,
    DROP COLUMN IF EXISTS source_url,
    DROP COLUMN IF EXISTS source_document_id,
    DROP COLUMN IF EXISTS source_assignment_id,
    DROP COLUMN IF EXISTS source_system;

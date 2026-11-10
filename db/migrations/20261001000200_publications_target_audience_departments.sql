-- +goose Up
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS target_department_id BIGINT,
    ADD COLUMN IF NOT EXISTS target_ministry_ids BIGINT[],
    ADD COLUMN IF NOT EXISTS target_department_ids BIGINT[];

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS department_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_publications_target_department
    ON publications(target_department_id)
    WHERE deleted_at IS NULL AND target_audience = 'department';

CREATE INDEX IF NOT EXISTS idx_users_department
    ON users(department_id);

-- +goose Down
DROP INDEX IF EXISTS idx_users_department;
DROP INDEX IF EXISTS idx_publications_target_department;

ALTER TABLE publications
    DROP COLUMN IF EXISTS target_department_ids,
    DROP COLUMN IF EXISTS target_ministry_ids,
    DROP COLUMN IF EXISTS target_department_id;

-- +goose Up
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS target_ministry_id BIGINT,
    ADD COLUMN IF NOT EXISTS ministry_id BIGINT,
    ADD COLUMN IF NOT EXISTS state_body_id BIGINT,
    ADD COLUMN IF NOT EXISTS department_id BIGINT;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS ministry_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_publications_target_ministry
    ON publications(target_ministry_id)
    WHERE deleted_at IS NULL AND target_audience = 'ministry';

CREATE INDEX IF NOT EXISTS idx_users_ministry
    ON users(ministry_id);

-- +goose Down
DROP INDEX IF EXISTS idx_users_ministry;
DROP INDEX IF EXISTS idx_publications_target_ministry;

ALTER TABLE users
    DROP COLUMN IF EXISTS ministry_id;

ALTER TABLE publications
    DROP COLUMN IF EXISTS department_id,
    DROP COLUMN IF EXISTS state_body_id,
    DROP COLUMN IF EXISTS ministry_id,
    DROP COLUMN IF EXISTS target_ministry_id;

-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS merged_into_id BIGINT REFERENCES users(id);

CREATE INDEX IF NOT EXISTS idx_users_merged_into_id
    ON users(merged_into_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_merged_into_id;
ALTER TABLE users DROP COLUMN IF EXISTS merged_into_id;
-- +goose StatementEnd

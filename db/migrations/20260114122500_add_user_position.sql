-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN position VARCHAR(255);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS position;
-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin
ALTER TABLE events ADD COLUMN is_online BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events DROP COLUMN is_online;
-- +goose StatementEnd

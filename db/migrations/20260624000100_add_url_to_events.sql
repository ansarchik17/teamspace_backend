-- +goose Up
-- +goose StatementBegin
ALTER TABLE events ADD COLUMN IF NOT EXISTS url TEXT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events DROP COLUMN IF EXISTS url;
-- +goose StatementEnd

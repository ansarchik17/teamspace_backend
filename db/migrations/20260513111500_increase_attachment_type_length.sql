-- +goose Up
-- +goose StatementBegin
ALTER TABLE messages ALTER COLUMN attachment_type TYPE VARCHAR(255);
ALTER TABLE channel_posts ALTER COLUMN attachment_type TYPE VARCHAR(255);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE messages ALTER COLUMN attachment_type TYPE VARCHAR(50);
ALTER TABLE channel_posts ALTER COLUMN attachment_type TYPE VARCHAR(64);
-- +goose StatementEnd

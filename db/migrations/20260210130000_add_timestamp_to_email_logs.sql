-- +goose Up
-- +goose StatementBegin
ALTER TABLE email_logs ADD COLUMN timestamp TIMESTAMP WITH TIME ZONE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE email_logs DROP COLUMN timestamp;
-- +goose StatementEnd

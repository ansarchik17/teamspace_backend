-- +goose Up
-- +goose StatementBegin
ALTER TABLE messages ADD COLUMN is_forwarded BOOLEAN DEFAULT FALSE;
ALTER TABLE messages ADD COLUMN original_sender_id BIGINT REFERENCES users(id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE messages DROP COLUMN original_sender_id;
ALTER TABLE messages DROP COLUMN is_forwarded;
-- +goose StatementEnd

-- +goose Up
ALTER TABLE quick_pin_credential
    ADD COLUMN IF NOT EXISTS client_id VARCHAR(100);

-- +goose Down
ALTER TABLE quick_pin_credential
    DROP COLUMN IF EXISTS client_id;

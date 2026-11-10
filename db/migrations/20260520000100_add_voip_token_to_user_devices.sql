-- +goose Up
ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS voip_token TEXT;

-- +goose Down
ALTER TABLE user_devices
    DROP COLUMN IF EXISTS voip_token;

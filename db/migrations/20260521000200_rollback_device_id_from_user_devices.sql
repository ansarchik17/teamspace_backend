-- +goose Up
DROP INDEX IF EXISTS idx_user_devices_user_device_id;

ALTER TABLE user_devices
    DROP COLUMN IF EXISTS device_id;

-- +goose Down
ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS device_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_user_device_id
    ON user_devices(user_id, device_id)
    WHERE device_id IS NOT NULL;

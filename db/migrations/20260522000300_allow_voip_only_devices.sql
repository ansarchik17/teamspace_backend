-- +goose Up
ALTER TABLE user_devices
    ALTER COLUMN fcm_token DROP NOT NULL;

DROP INDEX IF EXISTS idx_user_devices_fcm_token;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_fcm_token
    ON user_devices(fcm_token)
    WHERE fcm_token IS NOT NULL
      AND TRIM(fcm_token) <> '';

-- +goose Down
UPDATE user_devices
SET fcm_token = CONCAT('__voip_only__', id::text)
WHERE fcm_token IS NULL OR TRIM(fcm_token) = '';

DROP INDEX IF EXISTS idx_user_devices_fcm_token;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_fcm_token
    ON user_devices(fcm_token);

ALTER TABLE user_devices
    ALTER COLUMN fcm_token SET NOT NULL;

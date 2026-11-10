-- +goose Up
ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS platform VARCHAR(50);

ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS device_id TEXT;

ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS logged_out_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP;

UPDATE user_devices
SET platform = COALESCE(NULLIF(TRIM(platform), ''), NULLIF(TRIM(device_type), ''))
WHERE platform IS NULL OR TRIM(platform) = '';

UPDATE user_devices
SET updated_at = COALESCE(updated_at, created_at, CURRENT_TIMESTAMP);

WITH ranked_by_device AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY user_id, device_id
               ORDER BY COALESCE(last_used_at, created_at) DESC, created_at DESC, id DESC
           ) AS rn
    FROM user_devices
    WHERE device_id IS NOT NULL
      AND TRIM(device_id) <> ''
)
UPDATE user_devices ud
SET active = FALSE,
    logged_out_at = COALESCE(ud.logged_out_at, CURRENT_TIMESTAMP),
    updated_at = CURRENT_TIMESTAMP
FROM ranked_by_device r
WHERE ud.id = r.id
  AND r.rn > 1;

WITH ranked_by_token AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY fcm_token
               ORDER BY active DESC, COALESCE(last_used_at, created_at) DESC, created_at DESC, id DESC
           ) AS rn
    FROM user_devices
    WHERE NULLIF(TRIM(fcm_token), '') IS NOT NULL
)
DELETE FROM user_devices ud
USING ranked_by_token r
WHERE ud.id = r.id
  AND r.rn > 1;

DROP INDEX IF EXISTS idx_user_devices_token;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_fcm_token
    ON user_devices(fcm_token);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_user_device_id_active
    ON user_devices(user_id, device_id)
    WHERE device_id IS NOT NULL
      AND TRIM(device_id) <> ''
      AND active = TRUE;

CREATE INDEX IF NOT EXISTS idx_user_devices_user_active
    ON user_devices(user_id, active);

-- +goose Down
DROP INDEX IF EXISTS idx_user_devices_user_active;
DROP INDEX IF EXISTS idx_user_devices_user_device_id_active;
DROP INDEX IF EXISTS idx_user_devices_fcm_token;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_token
    ON user_devices(user_id, fcm_token);

ALTER TABLE user_devices
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE user_devices
    DROP COLUMN IF EXISTS logged_out_at;

ALTER TABLE user_devices
    DROP COLUMN IF EXISTS active;

ALTER TABLE user_devices
    DROP COLUMN IF EXISTS device_id;

ALTER TABLE user_devices
    DROP COLUMN IF EXISTS platform;

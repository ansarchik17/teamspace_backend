-- +goose Up
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS recurrence_interval INT,
    ADD COLUMN IF NOT EXISTS recurrence_until TIMESTAMPTZ NULL;

UPDATE events
SET recurrence_interval = 1
WHERE recurrence_interval IS NULL;

ALTER TABLE events
    ALTER COLUMN recurrence_interval SET DEFAULT 1,
ALTER COLUMN recurrence_interval SET NOT NULL;

-- +goose Down
ALTER TABLE events
DROP COLUMN IF EXISTS recurrence_until,
    DROP COLUMN IF EXISTS recurrence_interval;
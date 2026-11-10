-- +goose Up
ALTER TABLE event_attendees
    ADD COLUMN IF NOT EXISTS comment TEXT NULL;

ALTER TABLE event_attendees
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP NULL;

-- если хочешь, чтобы новые инвайты по умолчанию были pending
ALTER TABLE event_attendees
    ALTER COLUMN status SET DEFAULT 'pending';

-- чтобы старые записи не были с NULL created_at/updated_at (если нужно)
UPDATE event_attendees
SET updated_at = NOW()
WHERE updated_at IS NULL;

-- +goose Down
-- откат default
ALTER TABLE event_attendees
    ALTER COLUMN status DROP DEFAULT;

ALTER TABLE event_attendees
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE event_attendees
    DROP COLUMN IF EXISTS comment;
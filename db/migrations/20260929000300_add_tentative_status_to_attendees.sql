-- +goose Up
-- Calendar release migrations follow the deployed main migration sequence.
-- +goose StatementBegin

-- Add constraint for valid attendee statuses
ALTER TABLE event_attendees
    ADD CONSTRAINT event_attendees_status_check
    CHECK (status IN ('pending', 'accepted', 'declined', 'tentative')) NOT VALID;

-- Update any invalid statuses to pending (safety measure)
UPDATE event_attendees
SET status = 'pending'
WHERE status NOT IN ('pending', 'accepted', 'declined', 'tentative');

ALTER TABLE event_attendees VALIDATE CONSTRAINT event_attendees_status_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE event_attendees
    DROP CONSTRAINT IF EXISTS event_attendees_status_check;

-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_event_attendees_user_id ON event_attendees(user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_event_attendees_user_id;
-- +goose StatementEnd

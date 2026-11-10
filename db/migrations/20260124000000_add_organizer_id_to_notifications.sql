-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifications ADD COLUMN organizer_id BIGINT;
CREATE INDEX idx_notifications_organizer_id ON notifications(organizer_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_organizer_id;
ALTER TABLE notifications DROP COLUMN organizer_id;
-- +goose StatementEnd

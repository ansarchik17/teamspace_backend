-- +goose Up
-- +goose StatementBegin
ALTER TABLE alem_meet_participants DROP CONSTRAINT IF EXISTS alem_meet_participants_room_id_user_id_key;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE alem_meet_participants ADD CONSTRAINT alem_meet_participants_room_id_user_id_key UNIQUE(room_id, user_id);
-- +goose StatementEnd

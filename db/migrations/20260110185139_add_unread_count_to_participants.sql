-- +goose Up
-- +goose StatementBegin
ALTER TABLE conversation_participants ADD COLUMN unread_count INTEGER NOT NULL DEFAULT 0;

-- Optional: Initialize unread_count based on existing data (heavy operation, can be skipped for fresh starts or key manually)
-- UPDATE conversation_participants cp
-- SET unread_count = (
--    SELECT COUNT(*) 
--    FROM messages m 
--    WHERE m.conversation_id = cp.conversation_id 
--      AND m.created_at >= cp.joined_at 
--      AND m.sender_id != cp.user_id 
--      AND m.is_deleted = false
--      AND NOT EXISTS (
--          SELECT 1 FROM message_status ms 
--          WHERE ms.message_id = m.id AND ms.user_id = cp.user_id AND ms.read_at IS NOT NULL
--      )
-- );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE conversation_participants DROP COLUMN unread_count;
-- +goose StatementEnd

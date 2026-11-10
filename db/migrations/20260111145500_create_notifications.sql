-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS notifications (
                                             id UUID PRIMARY KEY,
                                             user_id BIGINT NOT NULL REFERENCES users(id),
                                             type VARCHAR(50) NOT NULL, -- например: 'calendar_invite', 'system', 'review'
                                             title VARCHAR(255) NOT NULL,
                                             content TEXT NOT NULL,
                                             is_read BOOLEAN DEFAULT FALSE,
                                             metadata JSONB, -- Для хранения ID события, ссылки и т.д.
                                             created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_id ON notifications(user_id);
CREATE INDEX idx_notifications_unread ON notifications(user_id) WHERE is_read = FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_unread;
DROP INDEX IF EXISTS idx_notifications_user_id;
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
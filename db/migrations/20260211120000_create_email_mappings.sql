-- +goose Up
-- +goose StatementBegin
CREATE TABLE email_mappings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    incoming_email TEXT NOT NULL UNIQUE, -- Email который приходит на сервер
    target_email TEXT NOT NULL,          -- Email куда нужно переслать
    user_id BIGINT REFERENCES users(id), -- Пользователь которому принадлежит mapping
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_email_mappings_incoming ON email_mappings(incoming_email) WHERE is_active = TRUE;
CREATE INDEX idx_email_mappings_user_id ON email_mappings(user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_email_mappings_user_id;
DROP INDEX IF EXISTS idx_email_mappings_incoming;
DROP TABLE IF EXISTS email_mappings;
-- +goose StatementEnd

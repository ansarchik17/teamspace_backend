-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN is_first_login BOOLEAN NOT NULL DEFAULT TRUE;

-- Если пользователь уже входил ранее, это не первый вход
UPDATE users SET is_first_login = FALSE WHERE last_login_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS is_first_login;
-- +goose StatementEnd
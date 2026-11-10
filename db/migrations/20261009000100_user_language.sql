-- +goose Up
ALTER TABLE users
    ADD COLUMN language VARCHAR(2) NOT NULL DEFAULT 'ru',
    ADD CONSTRAINT users_language_check CHECK (language IN ('ru', 'kk'));

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_language_check, DROP COLUMN language;

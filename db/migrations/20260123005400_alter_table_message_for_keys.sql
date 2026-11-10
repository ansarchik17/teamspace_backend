-- +goose Up
-- Добавляем колонку для хранения зашифрованных сессионных ключей для каждого устройства
ALTER TABLE messages ADD COLUMN encrypted_keys JSONB;

-- +goose Down
ALTER TABLE messages DROP COLUMN encrypted_keys;
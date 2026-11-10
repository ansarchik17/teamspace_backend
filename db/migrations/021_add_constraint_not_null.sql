-- +goose Up
ALTER TABLE quick_pin_credential
    ALTER COLUMN encrypted_session_secret DROP NOT NULL;

-- (опционально) если хочешь вообще убрать:
-- ALTER TABLE quick_pin_credential DROP COLUMN IF EXISTS encrypted_session_secret;

-- +goose Down
-- Если откатывать: верни NOT NULL (и/или колонку)
ALTER TABLE quick_pin_credential
    ALTER COLUMN encrypted_session_secret SET NOT NULL;
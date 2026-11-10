-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS keycloak_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS iin VARCHAR(12);

-- password_hash делаем nullable (если сейчас NOT NULL)
ALTER TABLE users
    ALTER COLUMN password_hash DROP NOT NULL;

-- Уникальность обычно нужна только когда значение реально задано:
-- (Postgres и так позволяет много NULL в UNIQUE, но "" (пустая строка) будет конфликтовать)
CREATE UNIQUE INDEX IF NOT EXISTS ux_users_keycloak_id
    ON users (keycloak_id)
    WHERE keycloak_id IS NOT NULL AND keycloak_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS ux_users_iin
    ON users (iin)
    WHERE iin IS NOT NULL AND iin <> '';

-- +goose Down
-- сначала индексы, потом колонки
DROP INDEX IF EXISTS ux_users_iin;
DROP INDEX IF EXISTS ux_users_keycloak_id;

ALTER TABLE users
DROP COLUMN IF EXISTS iin,
    DROP COLUMN IF EXISTS keycloak_id;
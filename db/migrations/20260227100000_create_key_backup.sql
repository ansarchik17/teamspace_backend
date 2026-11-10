-- +goose Up

-- Таблица для хранения зашифрованного бэкапа приватного ключа пользователя.
-- Приватный ключ шифруется на стороне клиента паролем/PIN-кодом пользователя
-- перед отправкой на сервер. Сервер хранит только зашифрованный blob.
--
-- Схема:
--   - user_id: владелец ключа
--   - encrypted_private_key: приватный ключ, зашифрованный AES-256-GCM с ключом, 
--                            выведенным из мастер-пароля пользователя (PBKDF2/Argon2)
--   - salt: случайная соль, используемая при выводе ключа шифрования из пароля
--   - iv:   Initialization Vector (nonce) для AES-GCM
--   - kdf_algorithm: алгоритм вывода ключа (argon2id, pbkdf2). Для будущей совместимости.
--   - version: версия схемы (для миграции при смене алгоритма)
--   - created_at / updated_at: метки времени

CREATE TABLE user_key_backups (
    user_id          BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Зашифрованный клиентом приватный ключ (Base64)
    encrypted_private_key TEXT   NOT NULL,
    -- Соль для KDF (Base64)
    salt             TEXT        NOT NULL,
    -- IV/Nonce для AES-GCM (Base64)
    iv               TEXT        NOT NULL,
    -- Алгоритм вывода ключа: 'argon2id' или 'pbkdf2'
    kdf_algorithm    VARCHAR(20) NOT NULL DEFAULT 'argon2id',
    -- Версия схемы шифрования - полезно при смене алгоритма
    version          INT         NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id)
);

COMMENT ON TABLE user_key_backups IS
  'Зашифрованный бэкап приватного ключа E2EE. '
  'Расшифровать может только сам пользователь, зная свой мастер-пароль.';

-- +goose Down
DROP TABLE user_key_backups;

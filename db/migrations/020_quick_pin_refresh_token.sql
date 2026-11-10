-- +goose Up

-- 1) Добавляем новое поле для refresh токена (как в Java)
ALTER TABLE quick_pin_credential
    ADD COLUMN IF NOT EXISTS encrypted_refresh_token TEXT;

-- 2) На всякий: created_at / updated_at если их нет
ALTER TABLE quick_pin_credential
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- 3) Индекс/уникальность на (user_id, device_id) чтобы Upsert работал стабильно
-- (если у тебя в репо Upsert идет через ON CONFLICT (user_id, device_id))
CREATE UNIQUE INDEX IF NOT EXISTS ux_quick_pin_user_device
    ON quick_pin_credential (user_id, device_id);

-- 4) (Опционально) если хочешь хранить только 1 активный PIN на устройство:
-- можно сделать частичный уникальный индекс active=true
-- Но тогда Upsert должен быть аккуратный. Обычно достаточно (user_id, device_id).
-- CREATE UNIQUE INDEX IF NOT EXISTS ux_quick_pin_user_device_active
--     ON quick_pin_credential (user_id, device_id)
--     WHERE active = true;

-- +goose Down

DROP INDEX IF EXISTS ux_quick_pin_user_device;

ALTER TABLE quick_pin_credential
DROP COLUMN IF EXISTS encrypted_refresh_token;

-- created_at/updated_at Down обычно не откатывают, но можно:
-- ALTER TABLE quick_pin_credential DROP COLUMN IF EXISTS created_at;
-- ALTER TABLE quick_pin_credential DROP COLUMN IF EXISTS updated_at;
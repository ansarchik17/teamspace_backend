-- +goose Up
-- Таблица персональных аудио-записей звонков (каждый участник пишет сам себя)
CREATE TABLE IF NOT EXISTS personal_call_recordings (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id          uuid        NOT NULL REFERENCES call_history(call_id) ON DELETE CASCADE,
    room_name        text        NOT NULL,
    owner_user_id    bigint      NOT NULL,  -- кто запустил запись
    egress_id        text        NOT NULL UNIQUE,
    status           text        NOT NULL DEFAULT 'recording',  -- recording | completed | stopped | failed
    started_at       timestamptz NOT NULL DEFAULT now(),
    stopped_at       timestamptz,
    file_path        text,                   -- S3 key (заполняется после завершения egress)
    duration_sec     int,                    -- длительность в секундах
    failure_reason   text,                   -- причина ошибки (если status=failed)
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Индекс для пагинации личных записей пользователя
CREATE INDEX IF NOT EXISTS idx_pcr_owner_created
    ON personal_call_recordings (owner_user_id, created_at DESC);

-- Индекс для быстрого поиска активных записей по звонку
CREATE INDEX IF NOT EXISTS idx_pcr_call_status
    ON personal_call_recordings (call_id, status)
    WHERE status = 'recording';

-- Уникальность: один пользователь — одна активная запись на один звонок
-- Реализуется через partial unique index
CREATE UNIQUE INDEX IF NOT EXISTS uq_pcr_active_per_user_call
    ON personal_call_recordings (call_id, owner_user_id)
    WHERE status = 'recording';

-- +goose Down
DROP TABLE IF EXISTS personal_call_recordings;

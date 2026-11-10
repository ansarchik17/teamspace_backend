-- +goose Up
-- Таблица для хранения аудио-записей звонков
CREATE TABLE IF NOT EXISTS call_recordings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id     uuid NOT NULL REFERENCES call_history(call_id) ON DELETE CASCADE,
    room_id     text NOT NULL,
    egress_id   text NOT NULL UNIQUE,
    status      text NOT NULL DEFAULT 'recording', -- recording | completed | failed | stopped
    file_path   text,      -- путь к файлу (S3 key или локальный путь)
    file_url    text,      -- presigned URL (заполняется после завершения)
    started_by  bigint NOT NULL, -- user_id кто начал запись
    started_at  timestamptz NOT NULL DEFAULT now(),
    stopped_at  timestamptz,
    duration_s  int,       -- длительность в секундах (после завершения)
    size_bytes  bigint,    -- размер файла в байтах (после завершения)
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_call_recordings_call_id
    ON call_recordings (call_id);

CREATE INDEX IF NOT EXISTS idx_call_recordings_egress_id
    ON call_recordings (egress_id);

CREATE INDEX IF NOT EXISTS idx_call_recordings_status
    ON call_recordings (status)
    WHERE status = 'recording';

-- +goose Down
DROP TABLE IF EXISTS call_recordings;

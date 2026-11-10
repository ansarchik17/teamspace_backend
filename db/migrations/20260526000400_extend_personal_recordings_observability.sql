-- +goose Up
ALTER TABLE personal_call_recordings
    ADD COLUMN IF NOT EXISTS participant_identity text,
    ADD COLUMN IF NOT EXISTS failed_at timestamptz,
    ADD COLUMN IF NOT EXISTS livekit_error_code integer,
    ADD COLUMN IF NOT EXISTS livekit_error_message text,
    ADD COLUMN IF NOT EXISTS end_reason text,
    ADD COLUMN IF NOT EXISTS minio_object_exists boolean;

CREATE INDEX IF NOT EXISTS idx_pcr_status_updated_at
    ON personal_call_recordings (status, updated_at);

-- +goose Down
DROP INDEX IF EXISTS idx_pcr_status_updated_at;

ALTER TABLE personal_call_recordings
    DROP COLUMN IF EXISTS minio_object_exists,
    DROP COLUMN IF EXISTS end_reason,
    DROP COLUMN IF EXISTS livekit_error_message,
    DROP COLUMN IF EXISTS livekit_error_code,
    DROP COLUMN IF EXISTS failed_at,
    DROP COLUMN IF EXISTS participant_identity;

-- +goose Up
UPDATE personal_call_recordings
SET status = 'active',
    updated_at = now()
WHERE status = 'recording';

UPDATE personal_call_recordings
SET status = 'stopping',
    updated_at = now()
WHERE status = 'processing';

DROP INDEX IF EXISTS idx_pcr_call_status;
CREATE INDEX IF NOT EXISTS idx_pcr_call_live_status
    ON personal_call_recordings (call_id, status)
    WHERE status IN ('starting', 'active');

DROP INDEX IF EXISTS uq_pcr_active_per_user_call;
CREATE UNIQUE INDEX IF NOT EXISTS uq_pcr_lock_per_user_call
    ON personal_call_recordings (call_id, owner_user_id)
    WHERE status IN ('starting', 'active', 'stopping');

-- +goose Down
DROP INDEX IF EXISTS uq_pcr_lock_per_user_call;
CREATE UNIQUE INDEX IF NOT EXISTS uq_pcr_active_per_user_call
    ON personal_call_recordings (call_id, owner_user_id)
    WHERE status = 'recording';

DROP INDEX IF EXISTS idx_pcr_call_live_status;
CREATE INDEX IF NOT EXISTS idx_pcr_call_status
    ON personal_call_recordings (call_id, status)
    WHERE status = 'recording';

UPDATE personal_call_recordings
SET status = 'processing',
    updated_at = now()
WHERE status = 'stopping';

UPDATE personal_call_recordings
SET status = 'recording',
    updated_at = now()
WHERE status = 'active';

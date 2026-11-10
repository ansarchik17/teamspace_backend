-- +goose Up
UPDATE personal_call_recordings
SET status = 'failed',
    failure_reason = COALESCE(failure_reason, 'egress aborted before completion'),
    updated_at = now()
WHERE status = 'aborted';

-- +goose Down
UPDATE personal_call_recordings
SET status = 'aborted',
    updated_at = now()
WHERE status = 'failed'
  AND failure_reason = 'egress aborted before completion';

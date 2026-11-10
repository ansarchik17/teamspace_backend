-- +goose Up
-- Обновляем допустимую модель статусов для персональных записей:
-- stopped -> aborted
UPDATE personal_call_recordings
SET status = 'aborted',
    failure_reason = COALESCE(failure_reason, 'recording aborted before completion'),
    updated_at = now()
WHERE status = 'stopped';

-- +goose Down
UPDATE personal_call_recordings
SET status = 'stopped',
    updated_at = now()
WHERE status = 'aborted'
  AND failure_reason = 'recording aborted before completion';

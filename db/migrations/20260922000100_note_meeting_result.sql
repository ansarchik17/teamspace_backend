-- +goose Up
ALTER TABLE notes ADD COLUMN meeting_job_id TEXT;
CREATE UNIQUE INDEX notes_user_meeting_job_unique ON notes (user_id, meeting_job_id) WHERE meeting_job_id IS NOT NULL;

-- +goose Down
DROP INDEX notes_user_meeting_job_unique;
ALTER TABLE notes DROP COLUMN meeting_job_id;

-- +goose Up
ALTER TABLE files ADD COLUMN tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];
ALTER TABLE drive_activity_logs DROP CONSTRAINT drive_activity_logs_action_check;
ALTER TABLE drive_activity_logs ADD CONSTRAINT drive_activity_logs_action_check
    CHECK (action IN ('created','uploaded','renamed','moved','edited','deleted','copied','restored','downloaded'));
-- Public-link visitors have no authenticated user identity.
ALTER TABLE drive_activity_logs ALTER COLUMN actor_id DROP NOT NULL;

-- +goose Down
-- Refuse rollback when it would require deleting audit history.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM drive_activity_logs WHERE actor_id IS NULL OR action = 'downloaded') THEN
        RAISE EXCEPTION 'Cannot roll back DRV-008 while new audit events exist';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE drive_activity_logs ALTER COLUMN actor_id SET NOT NULL;
ALTER TABLE drive_activity_logs DROP CONSTRAINT drive_activity_logs_action_check;
ALTER TABLE drive_activity_logs ADD CONSTRAINT drive_activity_logs_action_check
    CHECK (action IN ('created','uploaded','renamed','moved','edited','deleted','copied','restored'));
ALTER TABLE files DROP COLUMN tags;

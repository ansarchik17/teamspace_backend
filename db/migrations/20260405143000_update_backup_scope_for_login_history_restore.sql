-- +goose Up

ALTER TABLE user_key_backups
    ALTER COLUMN backup_key_scope SET DEFAULT 'device_identity';

UPDATE user_key_backups
SET backup_key_scope = 'device_identity'
WHERE trim(lower(backup_key_scope)) IN ('', 'user_recovery_material');

COMMENT ON COLUMN user_key_backups.backup_key_scope IS
    'Semantic scope of the backup payload. device_identity means password-based restore may reuse the same private identity for old-history access.';

-- +goose Down

ALTER TABLE user_key_backups
    ALTER COLUMN backup_key_scope SET DEFAULT 'user_recovery_material';

UPDATE user_key_backups
SET backup_key_scope = 'user_recovery_material'
WHERE trim(lower(backup_key_scope)) = 'device_identity';

COMMENT ON COLUMN user_key_backups.backup_key_scope IS
    'Semantic scope of the backup payload. Used to distinguish recovery material from device onboarding semantics.';

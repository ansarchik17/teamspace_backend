-- +goose Up

ALTER TABLE user_key_backups
    ADD COLUMN IF NOT EXISTS backup_key_type VARCHAR(32) NOT NULL DEFAULT 'x25519',
    ADD COLUMN IF NOT EXISTS backup_key_format VARCHAR(64) NOT NULL DEFAULT 'pkcs8-der-base64',
    ADD COLUMN IF NOT EXISTS backup_key_scope VARCHAR(64) NOT NULL DEFAULT 'user_recovery_material',
    ADD COLUMN IF NOT EXISTS backup_format_version INT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS cipher_algorithm VARCHAR(64) NOT NULL DEFAULT 'aes-256-gcm',
    ADD COLUMN IF NOT EXISTS cipher_tag_embedded BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS aad_mode VARCHAR(32) NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS password_processing VARCHAR(32) NOT NULL DEFAULT 'utf8-raw';

COMMENT ON COLUMN user_key_backups.backup_key_type IS
    'Canonical type of the private key material inside the encrypted backup payload.';
COMMENT ON COLUMN user_key_backups.backup_key_format IS
    'Canonical serialization of the private key material before AES-GCM encryption.';
COMMENT ON COLUMN user_key_backups.backup_key_scope IS
    'Semantic scope of the backup payload. Used to distinguish recovery material from device onboarding semantics.';
COMMENT ON COLUMN user_key_backups.backup_format_version IS
    'Version of the backup plaintext contract independent from legacy version.';
COMMENT ON COLUMN user_key_backups.cipher_algorithm IS
    'Symmetric cipher used for encrypted_private_key.';
COMMENT ON COLUMN user_key_backups.cipher_tag_embedded IS
    'Whether the AES-GCM authentication tag is embedded into encrypted_private_key.';
COMMENT ON COLUMN user_key_backups.aad_mode IS
    'Additional authenticated data mode used during encrypted_private_key encryption.';
COMMENT ON COLUMN user_key_backups.password_processing IS
    'Password normalization mode applied before KDF derivation.';

-- Existing rows stay readable through the same explicit contract.
UPDATE user_key_backups
SET backup_key_type = COALESCE(NULLIF(trim(lower(backup_key_type)), ''), 'x25519'),
    backup_key_format = COALESCE(NULLIF(trim(lower(backup_key_format)), ''), 'pkcs8-der-base64'),
    backup_key_scope = COALESCE(NULLIF(trim(lower(backup_key_scope)), ''), 'user_recovery_material'),
    backup_format_version = COALESCE(NULLIF(backup_format_version, 0), 1),
    cipher_algorithm = COALESCE(NULLIF(trim(lower(cipher_algorithm)), ''), 'aes-256-gcm'),
    cipher_tag_embedded = COALESCE(cipher_tag_embedded, TRUE),
    aad_mode = COALESCE(NULLIF(trim(lower(aad_mode)), ''), 'none'),
    password_processing = COALESCE(NULLIF(trim(lower(password_processing)), ''), 'utf8-raw');

-- +goose Down

ALTER TABLE user_key_backups
    DROP COLUMN IF EXISTS password_processing,
    DROP COLUMN IF EXISTS aad_mode,
    DROP COLUMN IF EXISTS cipher_tag_embedded,
    DROP COLUMN IF EXISTS cipher_algorithm,
    DROP COLUMN IF EXISTS backup_format_version,
    DROP COLUMN IF EXISTS backup_key_scope,
    DROP COLUMN IF EXISTS backup_key_format,
    DROP COLUMN IF EXISTS backup_key_type;

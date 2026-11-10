-- +goose Up

ALTER TABLE user_key_backups
    ADD COLUMN IF NOT EXISTS kdf_params JSONB;

COMMENT ON COLUMN user_key_backups.kdf_params IS
    'Algorithm-specific KDF parameters required to deterministically decrypt a key backup, for example PBKDF2 iterations or Argon2 memory/parallelism.';

-- Legacy v1 PBKDF2 backups in repository history were documented with SHA-256 and 310000 iterations.
-- Backfill the missing contract so web clients can reproduce the same derived key without guessing.
UPDATE user_key_backups
SET kdf_params = jsonb_build_object(
        'iterations', 310000,
        'hash', 'sha256',
        'key_length', 32
    )
WHERE kdf_params IS NULL
  AND version = 1
  AND lower(kdf_algorithm) IN ('pbkdf2', 'pbkdf2-sha256');

-- +goose Down

ALTER TABLE user_key_backups
    DROP COLUMN IF EXISTS kdf_params;

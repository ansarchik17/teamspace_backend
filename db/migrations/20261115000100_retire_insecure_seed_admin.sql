-- +goose Up
-- Recognize only the historical local bootstrap identity with its unchanged
-- public default credential. Rotated passwords and Keycloak-backed identities
-- are deliberately preserved. The legacy password below is comparison-only;
-- it is never assigned to a user.
WITH retired AS (
    UPDATE users
    SET is_active = false,
        password_hash = crypt(encode(gen_random_bytes(32), 'hex'), gen_salt('bf', 12)),
        updated_at = now()
    WHERE lower(email) = 'admin@admin.kz'
      AND lower(username) = 'admin'
      AND role = 'admin'
      AND COALESCE(keycloak_id::text, '') = ''
      AND CASE
          -- An already locked or otherwise non-bcrypt credential must not
          -- make a security migration fail while inspecting the old identity.
          WHEN password_hash ~ '^\$2a\$(0[4-9]|[12][0-9]|3[01])\$[./A-Za-z0-9]{53}$'
          THEN crypt('Qwerty12!', password_hash) = password_hash
          ELSE false
      END
    RETURNING id
), revoked AS (
    INSERT INTO user_credential_state(user_id, invalid_before)
    SELECT id, now() FROM retired
    ON CONFLICT (user_id) DO UPDATE
    SET invalid_before = GREATEST(user_credential_state.invalid_before, EXCLUDED.invalid_before)
    RETURNING user_id
)
INSERT INTO security_audit_events(action, outcome, object_type, object_id)
SELECT 'security.insecure_seed_admin.retired', 'success', 'user', user_id::text
FROM revoked;

-- +goose Down
-- Security remediation is intentionally irreversible: never reactivate the
-- old identity, restore its password, or make its previous sessions valid.
SELECT 1;

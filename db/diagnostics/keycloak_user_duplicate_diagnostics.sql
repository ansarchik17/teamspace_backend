-- Duplicate emails (case-insensitive)
SELECT LOWER(email) AS normalized_email, ARRAY_AGG(id ORDER BY id) AS user_ids, COUNT(*) AS duplicates
FROM users
WHERE email IS NOT NULL AND TRIM(email) <> ''
GROUP BY LOWER(email)
HAVING COUNT(*) > 1
ORDER BY duplicates DESC, normalized_email;

-- Duplicate IIN values
SELECT iin, ARRAY_AGG(id ORDER BY id) AS user_ids, COUNT(*) AS duplicates
FROM users
WHERE iin IS NOT NULL AND TRIM(iin) <> ''
GROUP BY iin
HAVING COUNT(*) > 1
ORDER BY duplicates DESC, iin;

-- Duplicate usernames after normalization
SELECT LOWER(REGEXP_REPLACE(TRIM(username), '\s+', '', 'g')) AS normalized_username,
       ARRAY_AGG(id ORDER BY id) AS user_ids,
       COUNT(*) AS duplicates
FROM users
WHERE username IS NOT NULL AND TRIM(username) <> ''
GROUP BY LOWER(REGEXP_REPLACE(TRIM(username), '\s+', '', 'g'))
HAVING COUNT(*) > 1
ORDER BY duplicates DESC, normalized_username;

-- Keycloak-linked users without IIN/email
SELECT id, keycloak_id, email, username, iin, created_at
FROM users
WHERE keycloak_id IS NOT NULL
  AND TRIM(keycloak_id) <> ''
  AND (email IS NULL OR TRIM(email) = '')
  AND (iin IS NULL OR TRIM(iin) = '')
ORDER BY created_at DESC;

-- Recently created Keycloak users that may be duplicates
SELECT id, keycloak_id, email, username, iin, created_at, merged_into_id
FROM users
WHERE keycloak_id IS NOT NULL
  AND created_at >= NOW() - INTERVAL '30 days'
ORDER BY created_at DESC;

-- Calls where callee may have a duplicate authenticated identity
SELECT ch.call_id,
       ch.to_user_id AS call_callee_user_id,
       callee.email AS callee_email,
       callee.iin AS callee_iin,
       callee.username AS callee_username,
       dup.id AS duplicate_user_id,
       dup.keycloak_id AS duplicate_keycloak_id,
       dup.email AS duplicate_email,
       dup.iin AS duplicate_iin,
       dup.username AS duplicate_username
FROM call_history ch
JOIN users callee ON callee.id = ch.to_user_id
JOIN users dup
  ON dup.id <> callee.id
 AND dup.deleted_at IS NULL
 AND (
      (callee.iin IS NOT NULL AND callee.iin <> '' AND dup.iin = callee.iin)
   OR (callee.email IS NOT NULL AND callee.email <> '' AND LOWER(dup.email) = LOWER(callee.email))
   OR (callee.username IS NOT NULL AND callee.username <> '' AND LOWER(REGEXP_REPLACE(TRIM(dup.username), '\s+', '', 'g')) = LOWER(REGEXP_REPLACE(TRIM(callee.username), '\s+', '', 'g')))
 )
WHERE ch.deleted_at IS NULL
ORDER BY ch.created_at DESC;

-- +goose Up
-- Older organization records could contain owner_id without the matching
-- users.organization_id. Restore the tenant membership without moving users
-- that are already assigned to a different organization.
WITH owned AS (
    SELECT DISTINCT ON (owner_id) owner_id, id
    FROM organizations
    WHERE owner_id IS NOT NULL
      AND status = 'active'
    ORDER BY owner_id, updated_at DESC, id
)
UPDATE users AS u
SET organization_id = o.id,
    team_id = NULL,
    manager_id = NULL,
    updated_at = NOW()
FROM owned AS o
WHERE o.owner_id = u.id
  AND u.organization_id IS NULL
  AND u.deleted_at IS NULL;

-- +goose Down
-- This is a data repair. Reverting it would recreate invalid ownership data.
SELECT 1;

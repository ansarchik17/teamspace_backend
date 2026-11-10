-- +goose Up
-- Profile administration writes users.position; access management keeps a tenant-local
-- position ID. Keep both representations in the same transaction for every write path.
-- The destination position keeps its own policies; old policies are never copied.
-- +goose StatementBegin
CREATE FUNCTION sync_employee_access_position() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    position_name text;
    destination_id uuid;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.position IS NOT DISTINCT FROM OLD.position
           AND NEW.organization_id IS NOT DISTINCT FROM OLD.organization_id
           AND NEW.deleted_at IS NOT DISTINCT FROM OLD.deleted_at THEN
            RETURN NEW;
        END IF;
    END IF;

    position_name := NULLIF(BTRIM(NEW.position), '');
    IF NEW.organization_id IS NULL OR NEW.deleted_at IS NOT NULL OR position_name IS NULL THEN
        DELETE FROM organization_access_assignments WHERE user_id = NEW.id;
        RETURN NEW;
    END IF;

    -- Resolve only within this company. A previously unknown title has no configured
    -- policies and therefore grants no permissions when position management is enabled.
    -- RETURNING also handles a concurrent insertion of the same normalized title.
    INSERT INTO organization_access_positions(id, organization_id, name, position_key)
    VALUES(gen_random_uuid(), NEW.organization_id, position_name, LOWER(position_name))
    ON CONFLICT(organization_id, position_key) DO UPDATE
        SET name = organization_access_positions.name
    RETURNING id INTO destination_id;

    INSERT INTO organization_access_assignments(organization_id, user_id, position_id)
    VALUES(NEW.organization_id, NEW.id, destination_id)
    ON CONFLICT(user_id) DO UPDATE
        SET organization_id = EXCLUDED.organization_id, position_id = EXCLUDED.position_id
        WHERE organization_access_assignments.organization_id IS DISTINCT FROM EXCLUDED.organization_id
           OR organization_access_assignments.position_id IS DISTINCT FROM EXCLUDED.position_id;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER users_sync_access_position
AFTER INSERT OR UPDATE OF position, organization_id, deleted_at ON users
FOR EACH ROW EXECUTE FUNCTION sync_employee_access_position();

-- Repair existing profile/assignment differences once. Keep all position records and
-- permission sets, including positions that no longer have any assigned employees.
INSERT INTO organization_access_positions(id, organization_id, name, position_key)
SELECT gen_random_uuid(), organization_id, MIN(BTRIM(position)), LOWER(BTRIM(position))
FROM users
WHERE organization_id IS NOT NULL AND deleted_at IS NULL AND NULLIF(BTRIM(position), '') IS NOT NULL
GROUP BY organization_id, LOWER(BTRIM(position))
ON CONFLICT(organization_id, position_key) DO NOTHING;

DELETE FROM organization_access_assignments a
USING users u
WHERE a.user_id = u.id
  AND (u.organization_id IS NULL OR u.deleted_at IS NOT NULL OR NULLIF(BTRIM(u.position), '') IS NULL);

INSERT INTO organization_access_assignments(organization_id, user_id, position_id)
SELECT u.organization_id, u.id, p.id
FROM users u
JOIN organization_access_positions p
  ON p.organization_id = u.organization_id AND p.position_key = LOWER(BTRIM(u.position))
WHERE u.deleted_at IS NULL
ON CONFLICT(user_id) DO UPDATE
    SET organization_id = EXCLUDED.organization_id, position_id = EXCLUDED.position_id
    WHERE organization_access_assignments.organization_id IS DISTINCT FROM EXCLUDED.organization_id
       OR organization_access_assignments.position_id IS DISTINCT FROM EXCLUDED.position_id;

-- +goose Down
DROP TRIGGER users_sync_access_position ON users;
DROP FUNCTION sync_employee_access_position();

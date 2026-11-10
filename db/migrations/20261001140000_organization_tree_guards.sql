-- +goose Up
-- Guard legacy writes as well as the organization API. All unit/assignment
-- mutations in a company use the same lock, so archive/move races are atomic.
-- +goose StatementBegin
CREATE FUNCTION validate_organization_unit_tree() RETURNS trigger AS $$
DECLARE
 previous_organization UUID;
 locked_organization UUID;
 parent_changed BOOLEAN := TRUE;
 manager_changed BOOLEAN := TRUE;
BEGIN
 IF TG_OP = 'UPDATE' THEN
  previous_organization := OLD.organization_id;
  parent_changed := NEW.parent_team_id IS DISTINCT FROM OLD.parent_team_id OR NEW.organization_id IS DISTINCT FROM OLD.organization_id OR (OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL);
  manager_changed := NEW.manager_id IS DISTINCT FROM OLD.manager_id OR NEW.organization_id IS DISTINCT FROM OLD.organization_id OR (OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL);
 END IF;
 FOR locked_organization IN SELECT DISTINCT value FROM unnest(ARRAY[NEW.organization_id,previous_organization]) value WHERE value IS NOT NULL ORDER BY value LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended(locked_organization::text,1003));
 END LOOP;
 IF TG_OP = 'UPDATE' AND NEW.organization_id IS DISTINCT FROM OLD.organization_id THEN
  IF EXISTS(SELECT 1 FROM teams WHERE parent_team_id=NEW.id AND deleted_at IS NULL AND organization_id IS DISTINCT FROM NEW.organization_id)
   OR EXISTS(SELECT 1 FROM users WHERE team_id=NEW.id AND deleted_at IS NULL AND organization_id IS DISTINCT FROM NEW.organization_id) THEN
   RAISE EXCEPTION 'organization unit cannot be moved to another organization while it has children or employees' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_OP = 'UPDATE' AND OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
  IF EXISTS(SELECT 1 FROM teams WHERE parent_team_id=NEW.id AND deleted_at IS NULL)
   OR EXISTS(SELECT 1 FROM users WHERE team_id=NEW.id AND deleted_at IS NULL) THEN
   RAISE EXCEPTION 'organization unit must be empty before archiving' USING ERRCODE='23514';
  END IF;
 END IF;
 IF NEW.deleted_at IS NOT NULL THEN RETURN NEW; END IF;
 IF parent_changed AND NEW.parent_team_id IS NOT NULL THEN
  IF NEW.parent_team_id=NEW.id THEN RAISE EXCEPTION 'organization unit cannot be its own parent' USING ERRCODE='23514'; END IF;
  IF NOT EXISTS(SELECT 1 FROM teams WHERE id=NEW.parent_team_id AND organization_id=NEW.organization_id AND deleted_at IS NULL) THEN
   RAISE EXCEPTION 'parent organization unit not found' USING ERRCODE='23514';
  END IF;
  IF EXISTS(
   WITH RECURSIVE ancestors AS (
    SELECT id,parent_team_id FROM teams WHERE id=NEW.parent_team_id AND organization_id=NEW.organization_id AND deleted_at IS NULL
    UNION
    SELECT t.id,t.parent_team_id FROM teams t JOIN ancestors a ON t.id=a.parent_team_id WHERE t.organization_id=NEW.organization_id AND t.deleted_at IS NULL
   ) SELECT 1 FROM ancestors WHERE id=NEW.id
  ) THEN RAISE EXCEPTION 'organization unit cannot be moved inside its own subtree' USING ERRCODE='23514'; END IF;
 END IF;
 IF manager_changed AND NEW.manager_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.manager_id AND organization_id=NEW.organization_id AND deleted_at IS NULL AND is_active=TRUE) THEN
  RAISE EXCEPTION 'manager is outside the current organization or inactive' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER organization_unit_tree_guard BEFORE INSERT OR UPDATE OF parent_team_id,organization_id,deleted_at,manager_id ON teams
 FOR EACH ROW EXECUTE FUNCTION validate_organization_unit_tree();

-- +goose StatementBegin
CREATE FUNCTION validate_employee_unit_assignment() RETURNS trigger AS $$
DECLARE
 previous_organization UUID;
 locked_organization UUID;
BEGIN
 IF TG_OP = 'UPDATE' THEN
  previous_organization := OLD.organization_id;
  IF NEW.team_id IS NOT DISTINCT FROM OLD.team_id AND NEW.organization_id IS NOT DISTINCT FROM OLD.organization_id AND NEW.deleted_at IS NOT DISTINCT FROM OLD.deleted_at THEN RETURN NEW; END IF;
 END IF;
 FOR locked_organization IN SELECT DISTINCT value FROM unnest(ARRAY[NEW.organization_id,previous_organization]) value WHERE value IS NOT NULL ORDER BY value LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended(locked_organization::text,1003));
 END LOOP;
 IF NEW.deleted_at IS NULL AND NEW.team_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM teams WHERE id=NEW.team_id AND organization_id=NEW.organization_id AND deleted_at IS NULL) THEN
  RAISE EXCEPTION 'organization unit not found for employee assignment' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER employee_unit_guard BEFORE INSERT OR UPDATE OF team_id,organization_id,deleted_at ON users
 FOR EACH ROW EXECUTE FUNCTION validate_employee_unit_assignment();

-- +goose Down
DROP TRIGGER employee_unit_guard ON users;
DROP FUNCTION validate_employee_unit_assignment();
DROP TRIGGER organization_unit_tree_guard ON teams;
DROP FUNCTION validate_organization_unit_tree();

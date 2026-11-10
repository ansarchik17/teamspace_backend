-- +goose Up
CREATE TABLE employee_contacts (
 user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 work_email TEXT CHECK (work_email IS NULL OR length(work_email) <= 254),
 phone TEXT CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{6,14}$'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX users_active_manager_idx ON users (organization_id, manager_id, id)
 WHERE deleted_at IS NULL AND is_active = TRUE;

-- +goose StatementBegin
CREATE FUNCTION validate_employee_manager() RETURNS trigger AS $$
DECLARE
 previous_organization UUID;
 locked_organization UUID;
BEGIN
 IF TG_OP = 'UPDATE' THEN
  previous_organization := OLD.organization_id;
  IF NEW.manager_id IS NOT DISTINCT FROM OLD.manager_id AND NEW.organization_id IS NOT DISTINCT FROM OLD.organization_id THEN RETURN NEW; END IF;
 END IF;
 -- Serialize hierarchy writes in a company, including legacy user-update endpoints.
 FOR locked_organization IN
  SELECT DISTINCT value FROM unnest(ARRAY[NEW.organization_id,previous_organization]) value WHERE value IS NOT NULL ORDER BY value
 LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended(locked_organization::text, 1002));
 END LOOP;
 IF TG_OP = 'UPDATE' AND NEW.organization_id IS DISTINCT FROM OLD.organization_id THEN
  IF EXISTS(SELECT 1 FROM users WHERE manager_id=NEW.id AND deleted_at IS NULL AND organization_id IS DISTINCT FROM NEW.organization_id) THEN
   RAISE EXCEPTION 'invalid organization transfer: reassign direct reports first' USING ERRCODE='23514';
  END IF;
 END IF;
 IF NEW.manager_id IS NULL THEN RETURN NEW; END IF;
 IF NEW.manager_id = NEW.id THEN RAISE EXCEPTION 'employee cannot be its own manager' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.manager_id AND organization_id=NEW.organization_id AND deleted_at IS NULL AND is_active=TRUE) THEN
  RAISE EXCEPTION 'manager is outside the current organization or inactive' USING ERRCODE='23514';
 END IF;
 IF EXISTS(
  WITH RECURSIVE chain AS (
   SELECT id,manager_id FROM users WHERE id=NEW.manager_id AND organization_id=NEW.organization_id AND deleted_at IS NULL
   UNION
   SELECT u.id,u.manager_id FROM users u JOIN chain c ON u.id=c.manager_id WHERE u.organization_id=NEW.organization_id AND u.deleted_at IS NULL
  ) SELECT 1 FROM chain WHERE id=NEW.id
 ) THEN RAISE EXCEPTION 'employee hierarchy cannot contain a cycle' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER employee_manager_guard BEFORE INSERT OR UPDATE OF manager_id,organization_id ON users
 FOR EACH ROW EXECUTE FUNCTION validate_employee_manager();

-- +goose Down
DROP TRIGGER employee_manager_guard ON users;
DROP FUNCTION validate_employee_manager();
DROP INDEX users_active_manager_idx;
DROP TABLE employee_contacts;

-- +goose Up
ALTER TABLE projects ADD COLUMN owner_id BIGINT;
UPDATE projects SET owner_id=created_by;
ALTER TABLE projects ALTER COLUMN owner_id SET NOT NULL;
CREATE INDEX projects_owner_idx ON projects(owner_id);
CREATE TABLE employee_offboardings (
 user_id BIGINT PRIMARY KEY REFERENCES users(id),
 organization_id UUID NOT NULL REFERENCES organizations(id),
 successor_id BIGINT NOT NULL REFERENCES users(id),
 actor_id BIGINT NOT NULL REFERENCES users(id),
 reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 result JSONB NOT NULL,
 resources JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 identity_sync_status TEXT NOT NULL DEFAULT 'pending' CHECK(identity_sync_status IN ('pending','complete')),
 identity_sync_attempts INT NOT NULL DEFAULT 0,
 identity_sync_next_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX employee_offboarding_pending_idx ON employee_offboardings(identity_sync_next_at) WHERE identity_sync_status='pending';
-- Prevent legacy admin updates or LDAP/SSO synchronization from undoing offboarding.
-- +goose StatementBegin
CREATE FUNCTION guard_offboarded_employee() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM employee_offboardings WHERE user_id=OLD.id) AND
 (NEW.is_active OR NEW.organization_id IS DISTINCT FROM OLD.organization_id OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at OR NEW.keycloak_id IS DISTINCT FROM OLD.keycloak_id) THEN
  RAISE EXCEPTION 'employee is offboarded' USING ERRCODE='P7601';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER employee_offboarding_guard BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION guard_offboarded_employee();
-- Prevent in-flight requests from creating or handing new work to an offboarded owner.
-- Only ownership changes are guarded: existing metadata/history remains readable.
-- +goose StatementBegin
CREATE FUNCTION guard_work_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='projects' THEN
  IF NEW.owner_id IS NULL THEN NEW.owner_id:=NEW.created_by; END IF;
 END IF;
 IF TG_OP='UPDATE' AND NEW.owner_id=OLD.owner_id THEN RETURN NEW; END IF;
 PERFORM id FROM users WHERE id=NEW.owner_id AND deleted_at IS NULL AND is_active=TRUE FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'work owner must be active' USING ERRCODE='P7601'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER project_work_owner_guard BEFORE INSERT OR UPDATE OF owner_id ON projects FOR EACH ROW EXECUTE FUNCTION guard_work_owner();
CREATE TRIGGER board_work_owner_guard BEFORE INSERT OR UPDATE OF owner_id ON boards FOR EACH ROW EXECUTE FUNCTION guard_work_owner();
CREATE TRIGGER folder_work_owner_guard BEFORE INSERT OR UPDATE OF owner_id ON folders FOR EACH ROW EXECUTE FUNCTION guard_work_owner();
CREATE TRIGGER file_work_owner_guard BEFORE INSERT OR UPDATE OF owner_id ON files FOR EACH ROW EXECUTE FUNCTION guard_work_owner();

-- +goose Down
DROP TRIGGER file_work_owner_guard ON files;
DROP TRIGGER folder_work_owner_guard ON folders;
DROP TRIGGER board_work_owner_guard ON boards;
DROP TRIGGER project_work_owner_guard ON projects;
DROP FUNCTION guard_work_owner();
DROP TRIGGER employee_offboarding_guard ON users;
DROP FUNCTION guard_offboarded_employee();
DROP TABLE employee_offboardings;
DROP INDEX projects_owner_idx;
ALTER TABLE projects DROP COLUMN owner_id;

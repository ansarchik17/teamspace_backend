-- +goose Up
CREATE TABLE administrative_identity_deletions (
 user_id bigint PRIMARY KEY REFERENCES users(id),
 keycloak_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 retry_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);
-- Deletion keeps foreign keys and historical records, and cannot be undone by
-- an SSO profile synchronization or the old suspension/reactivation endpoint.
-- +goose StatementBegin
CREATE FUNCTION preserve_administrative_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='users' THEN
  IF TG_OP='UPDATE' AND OLD.deleted_at IS NOT NULL THEN
   NEW.deleted_at := OLD.deleted_at;
   NEW.is_active := false;
  END IF;
  IF NEW.deleted_at IS NULL AND NEW.organization_id IS NOT NULL AND
     (TG_OP='INSERT' OR NEW.organization_id IS DISTINCT FROM OLD.organization_id) THEN
   PERFORM id FROM organizations WHERE id=NEW.organization_id AND status<>'deleted' FOR KEY SHARE;
   IF NOT FOUND THEN RAISE EXCEPTION 'organization unavailable' USING ERRCODE='23514'; END IF;
  END IF;
 ELSE
  IF OLD.status='deleted' AND NEW.status<>'deleted' THEN
   RAISE EXCEPTION 'organization deleted' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_deleted_user BEFORE INSERT OR UPDATE ON users
 FOR EACH ROW EXECUTE FUNCTION preserve_administrative_deletion();
CREATE TRIGGER preserve_deleted_organization BEFORE UPDATE ON organizations
 FOR EACH ROW EXECUTE FUNCTION preserve_administrative_deletion();

-- +goose Down
DROP TABLE administrative_identity_deletions;
DROP TRIGGER preserve_deleted_organization ON organizations;
DROP TRIGGER preserve_deleted_user ON users;
DROP FUNCTION preserve_administrative_deletion();

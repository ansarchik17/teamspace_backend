-- +goose Up
-- Keep historical user IDs and all their foreign keys. Release only login
-- identifiers, preserving their originals in a private, immutable archive.
CREATE TABLE retired_user_identities (
 user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
 email TEXT,
 username TEXT,
 iin TEXT,
 retired_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX retired_user_identity_email ON retired_user_identities(lower(btrim(email)));
CREATE INDEX retired_user_identity_username ON retired_user_identities(lower(regexp_replace(btrim(username),'\s+','','g')));
CREATE INDEX retired_user_identity_iin ON retired_user_identities(iin);

-- +goose StatementBegin
CREATE FUNCTION archive_retired_user_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.deleted_at IS NOT NULL THEN
  INSERT INTO retired_user_identities(user_id,email,username,iin,retired_at)
  VALUES(OLD.id,OLD.email,OLD.username,OLD.iin,NEW.deleted_at)
  ON CONFLICT(user_id) DO NOTHING;
  IF FOUND THEN
   NEW.email := 'deleted.' || gen_random_uuid()::text || '@invalid.local';
   NEW.username := 'deleted.' || gen_random_uuid()::text;
  ELSE
   NEW.email := OLD.email;
   NEW.username := OLD.username;
  END IF;
  NEW.iin := NULL;
  NEW.keycloak_id := OLD.keycloak_id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- Runs after preserve_deleted_user, so SSO synchronization cannot restore
-- identifiers or reactivate a deleted account.
CREATE TRIGGER retire_deleted_user_identity BEFORE UPDATE ON users
 FOR EACH ROW EXECUTE FUNCTION archive_retired_user_identity();

-- Existing deletions receive the same treatment as future UI deletions.
INSERT INTO administrative_identity_deletions(user_id,keycloak_id)
SELECT id,keycloak_id FROM users WHERE deleted_at IS NOT NULL AND NULLIF(keycloak_id,'') IS NOT NULL
ON CONFLICT(user_id) DO NOTHING;
UPDATE users SET updated_at=updated_at WHERE deleted_at IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION retired_user_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Retired user identity archive is immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER retired_user_identity_immutable BEFORE UPDATE OR DELETE ON retired_user_identities
 FOR EACH ROW EXECUTE FUNCTION retired_user_identity_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Identity reuse rollback requires an explicit migration preserving new accounts and historical identity archives';
END $$;
-- +goose StatementEnd

-- +goose Up
CREATE TABLE drive_storage_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    quota_bytes bigint NOT NULL DEFAULT 10737418240 CHECK (quota_bytes >= 0),
    max_upload_bytes bigint NOT NULL DEFAULT 1073741824 CHECK (max_upload_bytes BETWEEN 1 AND 1073741824)
);
INSERT INTO drive_storage_policy(singleton) VALUES(true);
CREATE TABLE drive_storage_accounts (
    user_id bigint PRIMARY KEY REFERENCES users(id),
    quota_bytes bigint CHECK (quota_bytes >= 0),
    max_upload_bytes bigint CHECK (max_upload_bytes BETWEEN 1 AND 1073741824),
    revision bigint NOT NULL DEFAULT 0
);
CREATE INDEX idx_drive_quota_owner ON files(owner_id) WHERE scope='drive';

-- Reused content in one file (e.g. restoring a revision) is charged once.
-- Copies are separate logical files, even when sharing the same storage object.
-- +goose StatementBegin
CREATE FUNCTION drive_file_charge(fid uuid, obj text, bytes bigint, file_scope text, deleted timestamptz)
RETURNS bigint LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN file_scope='drive' AND (deleted IS NULL OR deleted>statement_timestamp()-INTERVAL '720 hours')
 THEN COALESCE((SELECT SUM(n) FROM (
   SELECT object_name,MAX(size) n FROM (
     SELECT obj AS object_name,bytes AS size
     UNION ALL SELECT object_name,size FROM file_versions WHERE file_id=fid
   ) refs GROUP BY object_name
 ) unique_objects),0)::bigint ELSE 0 END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION drive_storage_usage(uid bigint)
RETURNS TABLE(active_bytes bigint,version_bytes bigint,trash_bytes bigint)
LANGUAGE sql STABLE AS $$
 WITH charges AS (
   SELECT size,deleted_at,drive_file_charge(id,object_name,size,scope,deleted_at) charge
   FROM files WHERE owner_id=uid AND scope='drive'
     AND (deleted_at IS NULL OR deleted_at>statement_timestamp()-INTERVAL '720 hours')
 ) SELECT
   COALESCE(SUM(size) FILTER(WHERE deleted_at IS NULL),0)::bigint,
   COALESCE(SUM(charge-size) FILTER(WHERE deleted_at IS NULL),0)::bigint,
   COALESCE(SUM(charge) FILTER(WHERE deleted_at IS NOT NULL),0)::bigint FROM charges
$$;
-- +goose StatementEnd

-- A row UPDATE serializes accounting, including REPEATABLE READ copy operations:
-- concurrent snapshots get a retryable conflict, never silently exceed the limit.
-- +goose StatementBegin
CREATE FUNCTION drive_assert_storage(uid bigint, upload_bytes bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE lim bigint; max_upload bigint; used bigint;
BEGIN
 INSERT INTO drive_storage_accounts(user_id) VALUES(uid) ON CONFLICT DO NOTHING;
 UPDATE drive_storage_accounts SET revision=revision+1 WHERE user_id=uid;
 SELECT COALESCE(a.quota_bytes,p.quota_bytes),COALESCE(a.max_upload_bytes,p.max_upload_bytes)
 INTO lim,max_upload FROM drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=uid;
 IF upload_bytes>max_upload THEN
   RAISE EXCEPTION 'Drive upload limit exceeded' USING ERRCODE='P7502',
     DETAIL=json_build_object('max_upload_bytes',max_upload,'requested_bytes',upload_bytes)::text;
 END IF;
 SELECT active_bytes+version_bytes+trash_bytes INTO used FROM drive_storage_usage(uid);
 IF used>lim THEN
   RAISE EXCEPTION 'Drive quota exceeded' USING ERRCODE='P7501',
     DETAIL=json_build_object('quota_bytes',lim,'required_bytes',used,'owner_id',uid)::text;
 END IF;
EXCEPTION WHEN serialization_failure OR deadlock_detected THEN
 RAISE EXCEPTION 'Drive storage changed concurrently; retry' USING ERRCODE='P7503';
END $$;
-- +goose StatementEnd

-- All paths are guarded: uploads, OnlyOffice, imports, Notes exports, copies,
-- ownership transfers and restoration. Metadata-only changes remain possible
-- even if an administrator lowers the quota below existing usage.
-- +goose StatementBegin
CREATE FUNCTION drive_guard_file_storage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE before_bytes bigint:=0; after_bytes bigint; upload_bytes bigint:=0;
BEGIN
 IF NEW.scope<>'drive' THEN RETURN NEW; END IF;
 IF NEW.size<0 THEN RAISE EXCEPTION 'negative file size' USING ERRCODE='23514'; END IF;
 IF TG_OP='UPDATE' AND OLD.owner_id=NEW.owner_id THEN
   before_bytes:=drive_file_charge(OLD.id,OLD.object_name,OLD.size,OLD.scope,OLD.deleted_at);
 END IF;
 after_bytes:=drive_file_charge(NEW.id,NEW.object_name,NEW.size,NEW.scope,NEW.deleted_at);
 IF after_bytes>before_bytes THEN
   IF NOT EXISTS(SELECT 1 FROM file_versions WHERE file_id=NEW.id AND object_name=NEW.object_name AND size>=NEW.size) THEN upload_bytes:=NEW.size; END IF;
   PERFORM drive_assert_storage(NEW.owner_id,upload_bytes);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER drive_file_storage_guard AFTER INSERT OR UPDATE OF owner_id,object_name,size,scope,deleted_at ON files
FOR EACH ROW EXECUTE FUNCTION drive_guard_file_storage();

-- +goose StatementBegin
CREATE FUNCTION drive_guard_version_storage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE f files%ROWTYPE;
BEGIN
 SELECT * INTO f FROM files WHERE id=NEW.file_id;
 IF f.scope<>'drive' OR (f.deleted_at IS NOT NULL AND f.deleted_at<=statement_timestamp()-INTERVAL '720 hours') THEN RETURN NEW; END IF;
 IF NEW.size<0 THEN RAISE EXCEPTION 'negative revision size' USING ERRCODE='23514'; END IF;
 IF (f.object_name=NEW.object_name AND f.size>=NEW.size) OR EXISTS(
   SELECT 1 FROM file_versions WHERE file_id=NEW.file_id AND id<>NEW.id AND object_name=NEW.object_name AND size>=NEW.size
 ) THEN RETURN NEW; END IF;
 PERFORM drive_assert_storage(f.owner_id,NEW.size);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER drive_version_storage_guard AFTER INSERT OR UPDATE OF file_id,object_name,size ON file_versions
FOR EACH ROW EXECUTE FUNCTION drive_guard_version_storage();

-- +goose Down
DROP TRIGGER IF EXISTS drive_version_storage_guard ON file_versions;
DROP TRIGGER IF EXISTS drive_file_storage_guard ON files;
DROP FUNCTION IF EXISTS drive_guard_version_storage();
DROP FUNCTION IF EXISTS drive_guard_file_storage();
DROP FUNCTION IF EXISTS drive_assert_storage(bigint,bigint);
DROP FUNCTION IF EXISTS drive_storage_usage(bigint);
DROP FUNCTION IF EXISTS drive_file_charge(uuid,text,bigint,text,timestamptz);
DROP INDEX IF EXISTS idx_drive_quota_owner;
DROP TABLE IF EXISTS drive_storage_accounts;
DROP TABLE IF EXISTS drive_storage_policy;

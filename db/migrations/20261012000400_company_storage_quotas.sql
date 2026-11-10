-- +goose Up
CREATE TABLE organization_storage_accounts (
    organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    quota_bytes BIGINT CHECK (quota_bytes BETWEEN 0 AND 1125899906842624),
    revision BIGINT NOT NULL DEFAULT 0,
    accounting_revision BIGINT NOT NULL DEFAULT 0
);

-- +goose StatementBegin
CREATE FUNCTION organization_storage_usage(oid uuid)
RETURNS TABLE(allocated_bytes bigint,used_bytes bigint) LANGUAGE sql STABLE AS $$
 SELECT COALESCE(SUM(CASE WHEN u.deleted_at IS NULL THEN COALESCE(a.quota_bytes,p.quota_bytes) ELSE 0 END),0)::bigint,
        COALESCE(SUM(s.active_bytes+s.version_bytes+s.trash_bytes),0)::bigint
 FROM users u CROSS JOIN drive_storage_policy p
 LEFT JOIN drive_storage_accounts a ON a.user_id=u.id
 CROSS JOIN LATERAL drive_storage_usage(u.id) s
 WHERE u.organization_id=oid
$$;
-- +goose StatementEnd

-- Serialize company accounting before checking the committed total. UPDATE
-- also forces repeatable-read transactions to retry instead of using stale usage.
-- +goose StatementBegin
CREATE FUNCTION organization_assert_storage(oid uuid,check_allocations boolean)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE lim bigint; allocated bigint; used bigint;
BEGIN
 IF oid IS NULL THEN RETURN; END IF;
 INSERT INTO organization_storage_accounts(organization_id) VALUES(oid) ON CONFLICT DO NOTHING;
 UPDATE organization_storage_accounts SET accounting_revision=accounting_revision+1 WHERE organization_id=oid RETURNING quota_bytes INTO lim;
 IF lim IS NULL THEN RETURN; END IF;
 SELECT allocated_bytes,used_bytes INTO allocated,used FROM organization_storage_usage(oid);
 IF check_allocations AND allocated>lim THEN
   RAISE EXCEPTION 'Company storage allocations exceed budget' USING ERRCODE='P7503';
 END IF;
 IF used>lim THEN
   RAISE EXCEPTION 'Company Drive quota exceeded' USING ERRCODE='P7501',
     DETAIL=json_build_object('quota_bytes',lim,'required_bytes',used)::text;
 END IF;
EXCEPTION WHEN serialization_failure OR deadlock_detected THEN
 RAISE EXCEPTION 'Company storage changed concurrently; retry' USING ERRCODE='P7503';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drive_assert_storage(uid bigint,upload_bytes bigint)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE lim bigint; max_upload bigint; used bigint; oid uuid;
BEGIN
 SELECT organization_id INTO oid FROM users WHERE id=uid FOR SHARE;
 PERFORM organization_assert_storage(oid,false);
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
     DETAIL=json_build_object('quota_bytes',lim,'required_bytes',used)::text;
 END IF;
EXCEPTION WHEN serialization_failure OR deadlock_detected THEN
 RAISE EXCEPTION 'Drive storage changed concurrently; retry' USING ERRCODE='P7503';
END $$;
-- +goose StatementEnd

-- Quotas cannot be bypassed by account provisioning or moving a user to a company.
-- +goose StatementBegin
CREATE FUNCTION organization_guard_storage_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oid uuid; old_oid uuid;
BEGIN
 IF TG_OP='UPDATE' THEN old_oid:=OLD.organization_id; END IF;
 FOR oid IN SELECT DISTINCT id FROM unnest(ARRAY[old_oid,NEW.organization_id]) id WHERE id IS NOT NULL ORDER BY id LOOP
   PERFORM organization_assert_storage(oid,true);
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER organization_storage_membership_guard AFTER INSERT OR UPDATE OF organization_id,deleted_at ON users
FOR EACH ROW EXECUTE FUNCTION organization_guard_storage_membership();

-- +goose StatementBegin
CREATE FUNCTION organization_guard_storage_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE uid bigint; oid uuid;
BEGIN
 IF TG_OP='DELETE' THEN uid:=OLD.user_id; ELSE uid:=NEW.user_id; END IF;
 SELECT organization_id INTO oid FROM users WHERE id=uid;
 PERFORM organization_assert_storage(oid,true);
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER organization_storage_allocation_guard AFTER INSERT OR UPDATE OF quota_bytes OR DELETE ON drive_storage_accounts
FOR EACH ROW EXECUTE FUNCTION organization_guard_storage_allocation();

-- +goose Down
DROP TRIGGER organization_storage_allocation_guard ON drive_storage_accounts;
DROP TRIGGER organization_storage_membership_guard ON users;
DROP FUNCTION organization_guard_storage_allocation();
DROP FUNCTION organization_guard_storage_membership();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drive_assert_storage(uid bigint,upload_bytes bigint)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE lim bigint; max_upload bigint; used bigint;
BEGIN
 INSERT INTO drive_storage_accounts(user_id) VALUES(uid) ON CONFLICT DO NOTHING;
 UPDATE drive_storage_accounts SET revision=revision+1 WHERE user_id=uid;
 SELECT COALESCE(a.quota_bytes,p.quota_bytes),COALESCE(a.max_upload_bytes,p.max_upload_bytes)
 INTO lim,max_upload FROM drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=uid;
 IF upload_bytes>max_upload THEN
   RAISE EXCEPTION 'Drive upload limit exceeded' USING ERRCODE='P7502',DETAIL=json_build_object('max_upload_bytes',max_upload,'requested_bytes',upload_bytes)::text;
 END IF;
 SELECT active_bytes+version_bytes+trash_bytes INTO used FROM drive_storage_usage(uid);
 IF used>lim THEN
   RAISE EXCEPTION 'Drive quota exceeded' USING ERRCODE='P7501',DETAIL=json_build_object('quota_bytes',lim,'required_bytes',used)::text;
 END IF;
EXCEPTION WHEN serialization_failure OR deadlock_detected THEN
 RAISE EXCEPTION 'Drive storage changed concurrently; retry' USING ERRCODE='P7503';
END $$;
-- +goose StatementEnd
DROP FUNCTION organization_assert_storage(uuid,boolean);
DROP FUNCTION organization_storage_usage(uuid);
DROP TABLE organization_storage_accounts;

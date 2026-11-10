-- +goose Up
ALTER TABLE organization_storage_accounts ADD COLUMN mail_quota_bytes bigint NOT NULL DEFAULT 0 CHECK(mail_quota_bytes>=0);
ALTER TABLE organization_storage_accounts ADD COLUMN mail_managed boolean NOT NULL DEFAULT false;
ALTER TABLE organization_storage_accounts ADD COLUMN mail_synced_revision bigint NOT NULL DEFAULT -1;
ALTER TABLE organization_storage_accounts ADD CONSTRAINT company_mail_budget CHECK(quota_bytes IS NULL OR mail_quota_bytes<=quota_bytes);
-- Mail capacity is reserved in full. SMTP cannot race with Drive usage.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION organization_storage_usage(oid uuid)
RETURNS TABLE(allocated_bytes bigint,used_bytes bigint) LANGUAGE sql STABLE AS $$
 SELECT COALESCE(SUM(CASE WHEN u.deleted_at IS NULL THEN COALESCE(a.quota_bytes,p.quota_bytes) ELSE 0 END),0)::bigint + COALESCE((SELECT mail_quota_bytes FROM organization_storage_accounts WHERE organization_id=oid),0),
 COALESCE(SUM(s.active_bytes+s.version_bytes+s.trash_bytes),0)::bigint + COALESCE((SELECT mail_quota_bytes FROM organization_storage_accounts WHERE organization_id=oid),0)
 FROM users u CROSS JOIN drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=u.id
 CROSS JOIN LATERAL drive_storage_usage(u.id) s WHERE u.organization_id=oid
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION organization_storage_usage(oid uuid)
RETURNS TABLE(allocated_bytes bigint,used_bytes bigint) LANGUAGE sql STABLE AS $$
 SELECT COALESCE(SUM(CASE WHEN u.deleted_at IS NULL THEN COALESCE(a.quota_bytes,p.quota_bytes) ELSE 0 END),0)::bigint,
 COALESCE(SUM(s.active_bytes+s.version_bytes+s.trash_bytes),0)::bigint
 FROM users u CROSS JOIN drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=u.id
 CROSS JOIN LATERAL drive_storage_usage(u.id) s WHERE u.organization_id=oid
$$;
-- +goose StatementEnd
ALTER TABLE organization_storage_accounts DROP CONSTRAINT company_mail_budget;
ALTER TABLE organization_storage_accounts DROP COLUMN mail_synced_revision,DROP COLUMN mail_managed,DROP COLUMN mail_quota_bytes;

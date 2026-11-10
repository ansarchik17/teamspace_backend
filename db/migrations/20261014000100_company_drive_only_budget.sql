-- +goose Up
-- Keep legacy mail settings for rollback; Workspace no longer reserves mail
-- capacity or synchronizes these settings with AxisMail.
ALTER TABLE organization_storage_accounts DROP CONSTRAINT company_mail_budget;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION organization_storage_usage(oid uuid)
RETURNS TABLE(allocated_bytes bigint,used_bytes bigint) LANGUAGE sql STABLE AS $$
 SELECT COALESCE(SUM(CASE WHEN u.deleted_at IS NULL THEN COALESCE(a.quota_bytes,p.quota_bytes) ELSE 0 END),0)::bigint,
        COALESCE(SUM(s.active_bytes+s.version_bytes+s.trash_bytes),0)::bigint
 FROM users u CROSS JOIN drive_storage_policy p
 LEFT JOIN drive_storage_accounts a ON a.user_id=u.id
 CROSS JOIN LATERAL drive_storage_usage(u.id) s
 WHERE u.organization_id=oid
$$;
-- +goose StatementEnd

-- +goose Down
-- Budgets changed after Up may be below their legacy mail reservation.
-- Preserve those records on rollback; the constraint checks future writes.
ALTER TABLE organization_storage_accounts ADD CONSTRAINT company_mail_budget
 CHECK(quota_bytes IS NULL OR mail_quota_bytes<=quota_bytes) NOT VALID;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION organization_storage_usage(oid uuid)
RETURNS TABLE(allocated_bytes bigint,used_bytes bigint) LANGUAGE sql STABLE AS $$
 SELECT COALESCE(SUM(CASE WHEN u.deleted_at IS NULL THEN COALESCE(a.quota_bytes,p.quota_bytes) ELSE 0 END),0)::bigint + COALESCE((SELECT mail_quota_bytes FROM organization_storage_accounts WHERE organization_id=oid),0),
        COALESCE(SUM(s.active_bytes+s.version_bytes+s.trash_bytes),0)::bigint + COALESCE((SELECT mail_quota_bytes FROM organization_storage_accounts WHERE organization_id=oid),0)
 FROM users u CROSS JOIN drive_storage_policy p
 LEFT JOIN drive_storage_accounts a ON a.user_id=u.id
 CROSS JOIN LATERAL drive_storage_usage(u.id) s
 WHERE u.organization_id=oid
$$;
-- +goose StatementEnd

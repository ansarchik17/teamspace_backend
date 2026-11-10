-- +goose Up
-- Serialize the one-time policy reset with live user/quota changes.
-- Usage, period boundaries and reservations are deliberately untouched.
LOCK TABLE users, organization_ai_user_quotas, user_ai_personal_quotas IN ACCESS EXCLUSIVE MODE;

-- A system migration has no human actor. Keep its before/after evidence
-- separately instead of fabricating an administrator in the existing audit.
CREATE TABLE ai_personal_quota_policy_resets (
 id BIGSERIAL PRIMARY KEY,
 policy_version TEXT NOT NULL,
 quota_kind TEXT NOT NULL CHECK (quota_kind IN ('organization', 'personal')),
 organization_id UUID,
 user_id BIGINT NOT NULL,
 before_value JSONB NOT NULL,
 after_value JSONB NOT NULL,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO ai_personal_quota_policy_resets(policy_version,quota_kind,organization_id,user_id,before_value,after_value)
SELECT '20261027000100','organization',organization_id,user_id,
 jsonb_build_object('weekly_quota_microcredits',weekly_quota_microcredits,'five_hour_limit_microcredits',five_hour_limit_microcredits,'revision',revision),
 jsonb_build_object('weekly_quota_microcredits',100000000000::bigint,'five_hour_limit_microcredits',20000000000::bigint,'revision',revision+1)
FROM organization_ai_user_quotas q
WHERE (weekly_quota_microcredits IS DISTINCT FROM 100000000000::bigint
 OR five_hour_limit_microcredits IS DISTINCT FROM 20000000000::bigint)
 AND EXISTS (SELECT 1 FROM users u WHERE u.id=q.user_id AND u.organization_id=q.organization_id AND u.deleted_at IS NULL);

INSERT INTO ai_personal_quota_policy_resets(policy_version,quota_kind,user_id,before_value,after_value)
SELECT '20261027000100','personal',user_id,
 jsonb_build_object('weekly_quota_microcredits',weekly_quota_microcredits,'five_hour_limit_microcredits',five_hour_limit_microcredits,'revision',revision),
 jsonb_build_object('weekly_quota_microcredits',100000000000::bigint,'five_hour_limit_microcredits',20000000000::bigint,'revision',revision+1)
FROM user_ai_personal_quotas
WHERE weekly_quota_microcredits IS DISTINCT FROM 100000000000::bigint
 OR five_hour_limit_microcredits IS DISTINCT FROM 20000000000::bigint;

-- Company quota revisions are incremented by the existing database trigger.
-- Historical assignments from former companies must remain released.
UPDATE organization_ai_user_quotas q
SET weekly_quota_microcredits=100000000000, five_hour_limit_microcredits=20000000000
WHERE (weekly_quota_microcredits IS DISTINCT FROM 100000000000::bigint
 OR five_hour_limit_microcredits IS DISTINCT FROM 20000000000::bigint)
 AND EXISTS (SELECT 1 FROM users u WHERE u.id=q.user_id AND u.organization_id=q.organization_id AND u.deleted_at IS NULL);

UPDATE user_ai_personal_quotas
SET weekly_quota_microcredits=100000000000, five_hour_limit_microcredits=20000000000,
 revision=revision+1, updated_at=now()
WHERE weekly_quota_microcredits IS DISTINCT FROM 100000000000::bigint
 OR five_hour_limit_microcredits IS DISTINCT FROM 20000000000::bigint;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Restoring earlier AI quotas requires an explicit policy migration; preserve audit and later administrator changes';
END $$;
-- +goose StatementEnd

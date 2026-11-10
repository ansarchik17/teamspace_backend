-- +goose Up
-- Allow AI accounting records to belong directly to organizations(id) rather than
-- requiring a pre-existing organization_ai_budgets row. This enables observe mode
-- accounting for companies and users whose financial budgets have not yet been configured.

ALTER TABLE organization_ai_weekly_anchors
    DROP CONSTRAINT organization_ai_weekly_anchors_organization_id_fkey,
    ADD CONSTRAINT organization_ai_weekly_anchors_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;

ALTER TABLE ai_usage_operations
    DROP CONSTRAINT ai_usage_operations_organization_id_fkey,
    ADD CONSTRAINT ai_usage_operations_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;

ALTER TABLE organization_ai_user_quotas
    DROP CONSTRAINT organization_ai_user_quotas_organization_id_fkey,
    ADD CONSTRAINT organization_ai_user_quotas_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;

ALTER TABLE organization_ai_user_weekly_usage
    DROP CONSTRAINT organization_ai_user_weekly_usage_organization_id_user_id_fkey,
    ADD CONSTRAINT organization_ai_user_weekly_usage_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
    ADD CONSTRAINT organization_ai_user_weekly_usage_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM organization_ai_weekly_anchors a WHERE NOT EXISTS(SELECT 1 FROM organization_ai_budgets b WHERE b.organization_id = a.organization_id))
       OR EXISTS(SELECT 1 FROM ai_usage_operations o WHERE NOT EXISTS(SELECT 1 FROM organization_ai_budgets b WHERE b.organization_id = o.organization_id))
       OR EXISTS(SELECT 1 FROM organization_ai_user_quotas q WHERE NOT EXISTS(SELECT 1 FROM organization_ai_budgets b WHERE b.organization_id = q.organization_id))
       OR EXISTS(SELECT 1 FROM organization_ai_user_weekly_usage u WHERE NOT EXISTS(SELECT 1 FROM organization_ai_user_quotas q WHERE q.organization_id = u.organization_id AND q.user_id = u.user_id)) THEN
        RAISE EXCEPTION 'Cannot restore foreign keys to organization_ai_budgets when unconfigured usage exists' USING ERRCODE='23514';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE organization_ai_user_weekly_usage
    DROP CONSTRAINT organization_ai_user_weekly_usage_organization_id_fkey,
    DROP CONSTRAINT organization_ai_user_weekly_usage_user_id_fkey,
    ADD CONSTRAINT organization_ai_user_weekly_usage_organization_id_user_id_fkey
        FOREIGN KEY (organization_id, user_id) REFERENCES organization_ai_user_quotas(organization_id, user_id) ON DELETE RESTRICT;

ALTER TABLE organization_ai_user_quotas
    DROP CONSTRAINT organization_ai_user_quotas_organization_id_fkey,
    ADD CONSTRAINT organization_ai_user_quotas_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organization_ai_budgets(organization_id) ON DELETE RESTRICT;

ALTER TABLE ai_usage_operations
    DROP CONSTRAINT ai_usage_operations_organization_id_fkey,
    ADD CONSTRAINT ai_usage_operations_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organization_ai_budgets(organization_id) ON DELETE RESTRICT;

ALTER TABLE organization_ai_weekly_anchors
    DROP CONSTRAINT organization_ai_weekly_anchors_organization_id_fkey,
    ADD CONSTRAINT organization_ai_weekly_anchors_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organization_ai_budgets(organization_id) ON DELETE RESTRICT;

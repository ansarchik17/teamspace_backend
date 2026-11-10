-- +goose Up
CREATE TABLE organization_admin_audit (
 id BIGSERIAL PRIMARY KEY,
 organization_id UUID NOT NULL REFERENCES organizations(id),
 actor_id BIGINT NOT NULL,
 action TEXT NOT NULL,
 object_type TEXT NOT NULL,
 object_id TEXT NOT NULL,
 before_value JSONB NOT NULL DEFAULT 'null',
 after_value JSONB NOT NULL DEFAULT 'null',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX organization_admin_audit_company_id_idx ON organization_admin_audit(organization_id,id DESC);
CREATE INDEX organization_admin_audit_id_idx ON organization_admin_audit(id DESC);
-- +goose Down
DROP TABLE organization_admin_audit;

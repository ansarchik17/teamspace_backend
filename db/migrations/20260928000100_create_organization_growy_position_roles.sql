-- Company-specific mapping from Workspace job titles to Growy's fixed roles.
-- A missing mapping is treated as Growy employee access by the SSO integration.
-- +goose Up
CREATE TABLE IF NOT EXISTS organization_growy_position_roles (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    position_key VARCHAR(255) NOT NULL,
    position_name VARCHAR(255) NOT NULL,
    growy_role VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, position_key),
    CONSTRAINT organization_growy_position_roles_valid_role
        CHECK (growy_role IN ('employee', 'manager', 'tenant_admin', 'issuer'))
);

CREATE INDEX IF NOT EXISTS idx_organization_growy_position_roles_org
    ON organization_growy_position_roles (organization_id, position_key);

-- +goose Down
DROP TABLE IF EXISTS organization_growy_position_roles;

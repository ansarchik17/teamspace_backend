-- +goose Up
CREATE TABLE IF NOT EXISTS organization_identity_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider_type VARCHAR(16) NOT NULL,
    alias VARCHAR(120) NOT NULL UNIQUE,
    display_name VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    settings JSONB NOT NULL DEFAULT '{}'::jsonb,
    secret_set BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    last_error TEXT,
    last_tested_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT organization_identity_providers_type_check CHECK (provider_type IN ('oidc', 'saml')),
    CONSTRAINT organization_identity_providers_status_check CHECK (status IN ('pending', 'active', 'error', 'disabled'))
);

CREATE INDEX IF NOT EXISTS idx_org_identity_providers_organization
    ON organization_identity_providers(organization_id);

CREATE INDEX IF NOT EXISTS idx_org_identity_providers_enabled
    ON organization_identity_providers(organization_id, enabled)
    WHERE enabled = TRUE;

COMMENT ON TABLE organization_identity_providers IS
    'External OIDC/SAML identity providers configured per customer organization; secrets remain in Keycloak.';

-- +goose Down
DROP INDEX IF EXISTS idx_org_identity_providers_enabled;
DROP INDEX IF EXISTS idx_org_identity_providers_organization;
DROP TABLE IF EXISTS organization_identity_providers;

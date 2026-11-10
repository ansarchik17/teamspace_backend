-- +goose Up
-- Corporate tenant structure.  This replaces the former public-sector
-- hierarchy with neutral organization/team relationships.
CREATE TABLE IF NOT EXISTS organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(120) NOT NULL UNIQUE,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'team',
    manager_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, parent_team_id, name)
);

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS manager_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_users_organization_id ON users(organization_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_users_team_id ON users(team_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_users_manager_id ON users(manager_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_teams_organization_id ON teams(organization_id);

COMMENT ON TABLE organizations IS 'Customer companies/tenants using Workspace';
COMMENT ON TABLE teams IS 'Customer-defined teams and nested units';

-- +goose Down
DROP INDEX IF EXISTS idx_teams_organization_id;
DROP INDEX IF EXISTS idx_users_manager_id;
DROP INDEX IF EXISTS idx_users_team_id;
DROP INDEX IF EXISTS idx_users_organization_id;
ALTER TABLE users
    DROP COLUMN IF EXISTS manager_id,
    DROP COLUMN IF EXISTS team_id,
    DROP COLUMN IF EXISTS organization_id;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS organizations;

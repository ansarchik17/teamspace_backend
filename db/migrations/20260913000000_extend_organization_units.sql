-- Configurable organization builder fields.
-- A company may use any number of nested units and any unit type.
-- +goose Up
ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS unit_type VARCHAR(64) NOT NULL DEFAULT 'team',
    ADD COLUMN IF NOT EXISTS description TEXT,
    ADD COLUMN IF NOT EXISTS display_order INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

UPDATE teams
SET unit_type = kind
WHERE kind IS NOT NULL AND kind <> '' AND unit_type = 'team';

CREATE INDEX IF NOT EXISTS idx_teams_parent_active
    ON teams(organization_id, parent_team_id, display_order, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_teams_manager_active
    ON teams(manager_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_organizations_owner_id ON organizations(owner_id);

-- +goose Down
DROP INDEX IF EXISTS idx_organizations_owner_id;
DROP INDEX IF EXISTS idx_teams_manager_active;
DROP INDEX IF EXISTS idx_teams_parent_active;
ALTER TABLE teams
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS display_order,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS unit_type;
ALTER TABLE organizations DROP COLUMN IF EXISTS owner_id;

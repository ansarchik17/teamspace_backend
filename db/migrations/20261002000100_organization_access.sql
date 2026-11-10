-- +goose Up
CREATE TABLE organization_access_positions (
 id uuid PRIMARY KEY, organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 name varchar(255) NOT NULL, position_key varchar(255) NOT NULL, revision bigint NOT NULL DEFAULT 1,
 UNIQUE(organization_id,position_key), UNIQUE(organization_id,id)
);
CREATE TABLE organization_access_apps (
 organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 application text NOT NULL CHECK(application IN ('odo','sd')), enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 0, PRIMARY KEY(organization_id,application)
);
CREATE TABLE organization_position_permissions (
 organization_id uuid NOT NULL, position_id uuid NOT NULL, application text NOT NULL CHECK(application IN ('odo','sd')),
 permissions text[] NOT NULL DEFAULT '{}', revision bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(organization_id,position_id,application),
 FOREIGN KEY(organization_id,position_id) REFERENCES organization_access_positions(organization_id,id) ON DELETE CASCADE
);
CREATE TABLE organization_access_assignments (
 organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, position_id uuid NOT NULL,
 FOREIGN KEY(organization_id,position_id) REFERENCES organization_access_positions(organization_id,id) ON DELETE CASCADE
);
CREATE TABLE organization_app_links (
 organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 application text NOT NULL CHECK(application='sd'), external_company_id uuid NOT NULL, is_tenant_root boolean NOT NULL,
 PRIMARY KEY(organization_id,application), UNIQUE(application,external_company_id)
);
CREATE TABLE organization_access_audit (
 id bigserial PRIMARY KEY, organization_id uuid NOT NULL REFERENCES organizations(id), actor_id bigint NOT NULL,
 action text NOT NULL, details jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
-- Import existing job titles independently for every tenant.
INSERT INTO organization_access_positions(id,organization_id,name,position_key)
SELECT gen_random_uuid(), organization_id, MIN(BTRIM(position)), LOWER(BTRIM(position))
FROM users WHERE organization_id IS NOT NULL AND deleted_at IS NULL AND NULLIF(BTRIM(position),'') IS NOT NULL
GROUP BY organization_id,LOWER(BTRIM(position));
INSERT INTO organization_access_assignments(organization_id,user_id,position_id)
SELECT u.organization_id,u.id,p.id FROM users u JOIN organization_access_positions p ON p.organization_id=u.organization_id AND p.position_key=LOWER(BTRIM(u.position)) WHERE u.deleted_at IS NULL;
-- +goose Down
DROP TABLE organization_access_assignments, organization_access_audit, organization_app_links, organization_position_permissions, organization_access_apps, organization_access_positions;

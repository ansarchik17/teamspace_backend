-- +goose Up
ALTER TABLE organization_growy_position_roles ADD COLUMN growy_roles TEXT[];
UPDATE organization_growy_position_roles SET growy_roles = ARRAY[growy_role];
ALTER TABLE organization_growy_position_roles ALTER COLUMN growy_roles SET NOT NULL;
ALTER TABLE organization_growy_position_roles ADD CONSTRAINT organization_growy_position_roles_valid_roles
    CHECK (cardinality(growy_roles) BETWEEN 1 AND 4
        AND growy_roles <@ ARRAY['employee', 'manager', 'tenant_admin', 'issuer']::TEXT[]
        AND array_position(growy_roles, NULL) IS NULL
        AND growy_role = growy_roles[1]);

-- +goose Down
ALTER TABLE organization_growy_position_roles DROP COLUMN growy_roles;

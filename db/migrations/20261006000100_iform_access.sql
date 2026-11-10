-- +goose Up
ALTER TABLE organization_access_apps DROP CONSTRAINT organization_access_apps_application_check;
ALTER TABLE organization_access_apps ADD CONSTRAINT organization_access_apps_application_check CHECK(application IN ('odo','sd','iform'));
ALTER TABLE organization_position_permissions DROP CONSTRAINT organization_position_permissions_application_check;
ALTER TABLE organization_position_permissions ADD CONSTRAINT organization_position_permissions_application_check CHECK(application IN ('odo','sd','iform'));

-- +goose Down
DELETE FROM organization_position_permissions WHERE application='iform';
DELETE FROM organization_access_apps WHERE application='iform';
ALTER TABLE organization_access_apps DROP CONSTRAINT organization_access_apps_application_check;
ALTER TABLE organization_access_apps ADD CONSTRAINT organization_access_apps_application_check CHECK(application IN ('odo','sd'));
ALTER TABLE organization_position_permissions DROP CONSTRAINT organization_position_permissions_application_check;
ALTER TABLE organization_position_permissions ADD CONSTRAINT organization_position_permissions_application_check CHECK(application IN ('odo','sd'));

-- +goose Up
-- Repair only the reported TTC tenant (tts.teamspace.kz). Explicit denials stay.
INSERT INTO organization_modules(organization_id,application,available,enabled)
SELECT o.id,apps.application,true,true FROM organizations o
CROSS JOIN (VALUES ('iform'),('odo'),('sd')) apps(application)
WHERE o.slug='tts' AND o.status<>'deleted'
ON CONFLICT(organization_id,application) DO NOTHING;

INSERT INTO organization_access_apps(organization_id,application,enabled)
SELECT o.id,apps.application,true FROM organizations o
CROSS JOIN (VALUES ('iform'),('odo'),('sd')) apps(application)
WHERE o.slug='tts' AND o.status<>'deleted'
ON CONFLICT(organization_id,application) DO NOTHING;

-- User-approved defaults for every future company, regardless of creation path.
-- +goose StatementBegin
CREATE FUNCTION initialize_company_service_access() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status<>'deleted' THEN
  INSERT INTO organization_modules(organization_id,application,available,enabled)
  SELECT NEW.id,application,true,true FROM (VALUES ('iform'),('odo'),('sd')) apps(application)
  ON CONFLICT(organization_id,application) DO NOTHING;
  INSERT INTO organization_access_apps(organization_id,application,enabled)
  SELECT NEW.id,application,true FROM (VALUES ('iform'),('odo'),('sd')) apps(application)
  ON CONFLICT(organization_id,application) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER organizations_initialize_service_access AFTER INSERT ON organizations
 FOR EACH ROW EXECUTE FUNCTION initialize_company_service_access();

-- +goose Down
-- Preserve settings administrators may already have changed.
DROP TRIGGER organizations_initialize_service_access ON organizations;
DROP FUNCTION initialize_company_service_access();

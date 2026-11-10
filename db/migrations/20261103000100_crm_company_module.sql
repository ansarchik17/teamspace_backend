-- +goose Up
ALTER TABLE organization_modules DROP CONSTRAINT organization_modules_application_check;
ALTER TABLE organization_modules ADD CONSTRAINT organization_modules_application_check
    CHECK (application IN ('iform','odo','sd','growy','crm'));

-- CRM previously used the learning module's gate. Preserve existing grants and
-- denials once, then allow administrators to manage CRM and learning separately.
INSERT INTO organization_modules(organization_id,application,available,enabled)
SELECT organization_id,'crm',available,enabled FROM organization_modules
WHERE application='growy'
ON CONFLICT(organization_id,application) DO NOTHING;

-- +goose Down
DELETE FROM organization_modules WHERE application='crm';
ALTER TABLE organization_modules DROP CONSTRAINT organization_modules_application_check;
ALTER TABLE organization_modules ADD CONSTRAINT organization_modules_application_check
    CHECK (application IN ('iform','odo','sd','growy'));

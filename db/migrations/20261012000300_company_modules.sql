-- +goose Up
CREATE TABLE organization_modules (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    application TEXT NOT NULL CHECK (application IN ('iform','odo','sd','growy')),
    available BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    PRIMARY KEY (organization_id, application)
);
-- Preserve existing integrations and their independent managed-permissions mode.
INSERT INTO organization_modules(organization_id,application,available,enabled)
SELECT id,application,TRUE,TRUE FROM organizations
CROSS JOIN (VALUES ('iform'),('odo'),('sd'),('growy')) AS modules(application);

-- +goose Down
DROP TABLE organization_modules;

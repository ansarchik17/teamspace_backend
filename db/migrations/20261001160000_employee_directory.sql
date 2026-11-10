-- +goose Up
CREATE TABLE employee_directory_links (
 organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 source TEXT NOT NULL CHECK(source IN ('file','ldap')),
 external_id TEXT NOT NULL CHECK(length(external_id) BETWEEN 1 AND 512),
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(organization_id,source,external_id),
 UNIQUE(organization_id,source,user_id)
);
CREATE TABLE employee_directory_configs (
 organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
 settings JSONB NOT NULL,
 encrypted_password BYTEA NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 last_sync_at TIMESTAMPTZ,
 last_sync_count INT NOT NULL DEFAULT 0,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose Down
DROP TABLE employee_directory_configs;
DROP TABLE employee_directory_links;

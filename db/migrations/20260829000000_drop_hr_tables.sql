-- +goose Up
DROP TABLE IF EXISTS user_hr_profiles CASCADE;
DROP TABLE IF EXISTS ekyzmet_sync_items CASCADE;
DROP TABLE IF EXISTS ekyzmet_sync_jobs CASCADE;
DROP TABLE IF EXISTS hr_org_mappings CASCADE;

-- +goose Down
-- Table drop migration - no down action needed.

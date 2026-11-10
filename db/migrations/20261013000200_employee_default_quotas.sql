-- +goose Up
-- Explicit employee overrides remain unchanged. Only the standard policy changes.
ALTER TABLE drive_storage_policy ALTER COLUMN quota_bytes SET DEFAULT 5368709120;
UPDATE drive_storage_policy SET quota_bytes=5368709120 WHERE singleton;

-- +goose Down
ALTER TABLE drive_storage_policy ALTER COLUMN quota_bytes SET DEFAULT 10737418240;
UPDATE drive_storage_policy SET quota_bytes=10737418240 WHERE singleton AND quota_bytes=5368709120;

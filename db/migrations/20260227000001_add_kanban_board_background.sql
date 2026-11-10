-- +goose Up
ALTER TABLE boards ADD COLUMN background_mode VARCHAR(20) NOT NULL DEFAULT 'preset_color';
ALTER TABLE boards ADD COLUMN background_preset_id VARCHAR(50) NOT NULL DEFAULT 'preset_default';
ALTER TABLE boards ADD COLUMN background_image_url TEXT;

-- +goose Down
ALTER TABLE boards DROP COLUMN background_mode;
ALTER TABLE boards DROP COLUMN background_preset_id;
ALTER TABLE boards DROP COLUMN background_image_url;

-- +goose Up
ALTER TABLE public_apps ADD COLUMN IF NOT EXISTS web_view BOOLEAN DEFAULT FALSE;

-- +goose Down
ALTER TABLE public_apps DROP COLUMN IF EXISTS web_view;

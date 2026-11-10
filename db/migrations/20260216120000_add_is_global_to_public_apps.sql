-- +goose Up
ALTER TABLE public_apps ADD COLUMN IF NOT EXISTS is_global BOOLEAN DEFAULT FALSE;

-- Set specific apps as global by default as requested
UPDATE public_apps SET is_global = TRUE WHERE id IN (35, 7);

-- +goose Down
ALTER TABLE public_apps DROP COLUMN IF EXISTS is_global;

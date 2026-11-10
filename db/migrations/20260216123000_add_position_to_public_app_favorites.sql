-- +goose Up
ALTER TABLE public_app_favorites ADD COLUMN IF NOT EXISTS position INT DEFAULT 0;

-- Initialize positions based on creation time for existing favorites
WITH ordered_favorites AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at) as rn
    FROM public_app_favorites
)
UPDATE public_app_favorites
SET position = ordered_favorites.rn
FROM ordered_favorites
WHERE public_app_favorites.id = ordered_favorites.id;

-- +goose Down
ALTER TABLE public_app_favorites DROP COLUMN IF EXISTS position;

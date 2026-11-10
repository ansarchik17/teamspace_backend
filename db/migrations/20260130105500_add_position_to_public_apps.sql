-- +goose Up
ALTER TABLE public_apps ADD COLUMN IF NOT EXISTS position INTEGER DEFAULT 0;

-- Update existing apps to have sequential positions
WITH ranked_apps AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY created_at) as rnk
    FROM public_apps
)
UPDATE public_apps
SET position = ranked_apps.rnk
FROM ranked_apps
WHERE public_apps.id = ranked_apps.id;

-- Make position NOT NULL after populating it
ALTER TABLE public_apps ALTER COLUMN position SET NOT NULL;

-- +goose Down
ALTER TABLE public_apps DROP COLUMN IF EXISTS position;

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public_apps
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_public_apps_category
    ON public_apps(category);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_public_apps_category;

ALTER TABLE public_apps
    DROP COLUMN IF EXISTS category;
-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public_apps
    ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

CREATE TABLE IF NOT EXISTS public_app_ratings (
    app_id BIGINT NOT NULL REFERENCES public_apps(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_public_app_ratings_app_id ON public_app_ratings(app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_public_app_ratings_app_id;
DROP TABLE IF EXISTS public_app_ratings;
ALTER TABLE public_apps DROP COLUMN IF EXISTS tags;
-- +goose StatementEnd

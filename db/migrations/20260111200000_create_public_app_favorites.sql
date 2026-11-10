-- +goose Up
CREATE TABLE IF NOT EXISTS public_app_favorites (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id BIGINT NOT NULL REFERENCES public_apps(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, app_id)
);

-- +goose Down
DROP TABLE IF EXISTS public_app_favorites;

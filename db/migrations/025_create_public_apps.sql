-- +goose Up
CREATE TABLE IF NOT EXISTS public_apps (
    id BIGSERIAL PRIMARY KEY,
    app_name TEXT NOT NULL,
    link TEXT NOT NULL,
    app_photo TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS public_apps;

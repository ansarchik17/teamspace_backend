-- +goose Up
-- Create table for storing per-user custom positions for public apps
CREATE TABLE IF NOT EXISTS public_app_user_positions (
    user_id BIGINT NOT NULL,
    app_id BIGINT NOT NULL,
    position INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, app_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (app_id) REFERENCES public_apps(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_public_app_user_positions_user_id ON public_app_user_positions(user_id);

-- +goose Down
DROP TABLE IF EXISTS public_app_user_positions;

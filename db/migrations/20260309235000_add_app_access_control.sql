-- +goose Up
-- +goose StatementBegin
ALTER TABLE public_apps ADD COLUMN is_private BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE public_app_access (
    app_id BIGINT NOT NULL REFERENCES public_apps(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (app_id, user_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public_app_access;
ALTER TABLE public_apps DROP COLUMN is_private;
-- +goose StatementEnd

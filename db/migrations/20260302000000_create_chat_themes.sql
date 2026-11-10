-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS chat_themes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    message_color_hex VARCHAR(20) DEFAULT NULL,
    background_type VARCHAR(50) NOT NULL DEFAULT 'default', -- 'color', 'image', 'default'
    background_color_hex VARCHAR(20) DEFAULT NULL,
    background_image_url TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS chat_themes;
-- +goose StatementEnd

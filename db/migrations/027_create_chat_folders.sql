-- +goose Up
-- +goose StatementBegin

CREATE TABLE chat_folders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE chat_folder_items (
    folder_id UUID NOT NULL REFERENCES chat_folders(id) ON DELETE CASCADE,
    chat_id UUID NOT NULL,
    chat_type VARCHAR(20) NOT NULL CHECK (chat_type IN ('conversation', 'group')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (folder_id, chat_id, chat_type)
);

CREATE INDEX idx_chat_folders_user ON chat_folders(user_id);
CREATE INDEX idx_chat_folder_items_folder ON chat_folder_items(folder_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS chat_folder_items;
DROP TABLE IF EXISTS chat_folders;

-- +goose StatementEnd

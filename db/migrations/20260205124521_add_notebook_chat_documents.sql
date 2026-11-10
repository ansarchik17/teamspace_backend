-- +goose Up
CREATE TABLE IF NOT EXISTS notebook_chat_documents (
    chat_id UUID NOT NULL REFERENCES notebook_chats(id) ON DELETE CASCADE,
    document_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    PRIMARY KEY (chat_id, document_id)
);

-- +goose Down
DROP TABLE IF EXISTS notebook_chat_documents;

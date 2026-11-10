-- +goose Up
-- Persist server-extracted document context with the AI message that uploaded it.
-- The original file remains in the existing file storage; this text is the
-- searchable context used for follow-up questions in the same conversation.
ALTER TABLE ai_messages
    ADD COLUMN IF NOT EXISTS attachment_name TEXT,
    ADD COLUMN IF NOT EXISTS attachment_mime_type TEXT,
    ADD COLUMN IF NOT EXISTS extracted_text TEXT;

-- +goose Down
ALTER TABLE ai_messages
    DROP COLUMN IF EXISTS extracted_text,
    DROP COLUMN IF EXISTS attachment_mime_type,
    DROP COLUMN IF EXISTS attachment_name;

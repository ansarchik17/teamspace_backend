-- +goose Up

-- Add rich content columns to notes table
ALTER TABLE notes
    ADD COLUMN content_doc  JSONB    NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN content_text TEXT     NOT NULL DEFAULT '';

-- Drop the old FTS generated column and create a new one covering content_text + title
ALTER TABLE notes DROP COLUMN IF EXISTS search_vector;

ALTER TABLE notes
    ADD COLUMN search_vector tsvector
        GENERATED ALWAYS AS (
            to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content_text, '') || ' ' || coalesce(content, ''))
        ) STORED;

-- Recreate FTS index on new search_vector
CREATE INDEX idx_notes_search_vector_v2 ON notes USING GIN (search_vector);

-- New note_photo_attachments table for presigned-URL based photo uploads
CREATE TABLE note_photo_attachments (
    id           UUID        PRIMARY KEY,
    note_id      UUID        NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id      BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    storage_key  TEXT        NOT NULL,
    mime_type    VARCHAR(100) NOT NULL,
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    width        INT         NULL,
    height       INT         NULL,
    status       TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'uploaded', 'deleted')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_note_photo_attachments_note_status  ON note_photo_attachments(note_id, status);
CREATE INDEX idx_note_photo_attachments_user_created ON note_photo_attachments(user_id, created_at DESC);

-- +goose Down
ALTER TABLE notes DROP COLUMN IF EXISTS content_doc;
ALTER TABLE notes DROP COLUMN IF EXISTS content_text;
ALTER TABLE notes DROP COLUMN IF EXISTS search_vector;

ALTER TABLE notes
    ADD COLUMN search_vector tsvector
        GENERATED ALWAYS AS (
            to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))
        ) STORED;

CREATE INDEX idx_notes_search_vector ON notes USING GIN (search_vector);
DROP INDEX IF EXISTS idx_notes_search_vector_v2;

DROP TABLE IF EXISTS note_photo_attachments;

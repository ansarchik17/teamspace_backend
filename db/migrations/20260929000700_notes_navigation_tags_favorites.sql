-- +goose Up
-- Follow the calendar release already applied in production.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION immutable_array_to_string(text[], text)
RETURNS text AS $$
    SELECT array_to_string($1, $2);
$$ LANGUAGE sql IMMUTABLE;
-- +goose StatementEnd

ALTER TABLE notes
    ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN is_favorite BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX idx_notes_tags ON notes USING GIN (tags);
CREATE INDEX idx_notes_user_favorite_updated ON notes(user_id, is_favorite, updated_at DESC, id DESC) WHERE is_favorite = TRUE;

ALTER TABLE notes DROP COLUMN IF EXISTS search_vector;

ALTER TABLE notes
    ADD COLUMN search_vector tsvector
        GENERATED ALWAYS AS (
            to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content_text, '') || ' ' || coalesce(content, '') || ' ' || coalesce(immutable_array_to_string(tags, ' '), ''))
        ) STORED;

CREATE INDEX idx_notes_search_vector_v3 ON notes USING GIN (search_vector);

-- +goose Down
DROP INDEX IF EXISTS idx_notes_search_vector_v3;
DROP INDEX IF EXISTS idx_notes_user_favorite_updated;
DROP INDEX IF EXISTS idx_notes_tags;

ALTER TABLE notes DROP COLUMN IF EXISTS search_vector;
ALTER TABLE notes DROP COLUMN IF EXISTS tags;
ALTER TABLE notes DROP COLUMN IF EXISTS is_favorite;

ALTER TABLE notes
    ADD COLUMN search_vector tsvector
        GENERATED ALWAYS AS (
            to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content_text, '') || ' ' || coalesce(content, ''))
        ) STORED;

CREATE INDEX idx_notes_search_vector_v2 ON notes USING GIN (search_vector);

DROP FUNCTION IF EXISTS immutable_array_to_string(text[], text);

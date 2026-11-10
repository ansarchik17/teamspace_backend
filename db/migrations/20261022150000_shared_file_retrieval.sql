-- +goose Up
CREATE TABLE ai_file_search_documents (
    file_id uuid PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    source_updated_at timestamptz NOT NULL,
    parser_version text NOT NULL DEFAULT 'retrieval-v2',
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','processing','ready','failed')),
    attempts int NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    lease_token uuid,
    text_chars int NOT NULL DEFAULT 0,
    revision text,
    error_code text,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_file_search_jobs ON ai_file_search_documents(state,next_attempt_at,updated_at);
CREATE TABLE ai_file_search_chunks (
    id uuid PRIMARY KEY,
    file_id uuid NOT NULL REFERENCES ai_file_search_documents(file_id) ON DELETE CASCADE,
    ordinal int NOT NULL,
    text text NOT NULL,
    search_simple tsvector GENERATED ALWAYS AS (to_tsvector('simple',text)) STORED,
    search_russian tsvector GENERATED ALWAYS AS (to_tsvector('russian',text)) STORED,
    locator jsonb NOT NULL DEFAULT '{}',
    UNIQUE(file_id,ordinal)
);
CREATE INDEX idx_ai_file_search_simple ON ai_file_search_chunks USING gin(search_simple);
CREATE INDEX idx_ai_file_search_russian ON ai_file_search_chunks USING gin(search_russian);

-- +goose Down
DROP TABLE ai_file_search_chunks;
DROP TABLE ai_file_search_documents;

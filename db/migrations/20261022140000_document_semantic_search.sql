-- +goose Up
-- No extension installation or database-image change during ordinary startup.
-- Semantic search is opt-in; existing deployments retain lexical search.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        CREATE TABLE IF NOT EXISTS ai_text_embeddings (
            model text NOT NULL,
            content_hash text NOT NULL,
            embedding vector,
            retry_after timestamptz,
            PRIMARY KEY (model, content_hash)
        );
    END IF;
END $$;
-- +goose StatementEnd

-- Russian morphology alongside exact terms/names/codes in the simple vector.
CREATE INDEX IF NOT EXISTS idx_ai_document_chunks_russian
    ON ai_document_chunks USING gin(to_tsvector('russian', text));

-- +goose Down
DROP INDEX IF EXISTS idx_ai_document_chunks_russian;
DROP TABLE IF EXISTS ai_text_embeddings;

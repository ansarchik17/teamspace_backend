-- Run as the DB administrator after installing pgvector in the PostgreSQL image.
-- This opt-in setup is also safe after the ordinary migration has already run.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS ai_text_embeddings (
    model text NOT NULL,
    content_hash text NOT NULL,
    embedding vector,
    retry_after timestamptz,
    PRIMARY KEY (model, content_hash)
);
-- Exact cosine search is intentional for small, ACL-filtered corpora.
-- Benchmark recall and filtered latency before introducing an ANN index.

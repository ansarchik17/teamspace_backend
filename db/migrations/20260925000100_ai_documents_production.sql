-- +goose Up
-- Durable document ingestion and grounded answer metadata for AI chats.
CREATE TABLE IF NOT EXISTS ai_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    declared_mime TEXT,
    detected_type TEXT NOT NULL,
    bytes BIGINT NOT NULL CHECK (bytes >= 0),
    sha256 CHAR(64) NOT NULL,
    object_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('uploading', 'uploaded', 'processing', 'ready', 'failed', 'deleted')),
    error_code TEXT,
    error_message TEXT,
    page_count INT,
    text_chars INT NOT NULL DEFAULT 0 CHECK (text_chars >= 0),
    parser_version TEXT NOT NULL DEFAULT 'v1',
    index_version TEXT NOT NULL DEFAULT 'v1',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (conversation_id, sha256)
);

CREATE INDEX IF NOT EXISTS idx_ai_documents_conversation_status
    ON ai_documents(conversation_id, status, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_ai_documents_owner ON ai_documents(owner_id, created_at DESC);

CREATE TABLE IF NOT EXISTS ai_document_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
    conversation_id UUID NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ordinal INT NOT NULL,
    text TEXT NOT NULL,
    search_vector TSVECTOR NOT NULL,
    locator JSONB NOT NULL DEFAULT '{}'::jsonb,
    token_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(document_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_ai_document_chunks_document ON ai_document_chunks(document_id, ordinal);
CREATE INDEX IF NOT EXISTS idx_ai_document_chunks_search ON ai_document_chunks USING GIN(search_vector);
CREATE INDEX IF NOT EXISTS idx_ai_document_chunks_scope ON ai_document_chunks(conversation_id, owner_id);

CREATE TABLE IF NOT EXISTS ai_document_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('queued', 'processing', 'completed', 'failed')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_document_jobs_active
    ON ai_document_jobs(document_id)
    WHERE state IN ('queued', 'processing');
CREATE INDEX IF NOT EXISTS idx_ai_document_jobs_queue
    ON ai_document_jobs(state, next_attempt_at, created_at);

CREATE TABLE IF NOT EXISTS ai_message_documents (
    message_id UUID NOT NULL REFERENCES ai_messages(id) ON DELETE CASCADE,
    document_id UUID NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(message_id, document_id)
);

CREATE TABLE IF NOT EXISTS ai_answer_sources (
    message_id UUID NOT NULL REFERENCES ai_messages(id) ON DELETE CASCADE,
    document_id UUID NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
    chunk_id UUID NOT NULL REFERENCES ai_document_chunks(id) ON DELETE CASCADE,
    rank INT NOT NULL,
    locator JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY(message_id, chunk_id)
);

-- +goose Down
DROP TABLE IF EXISTS ai_answer_sources;
DROP TABLE IF EXISTS ai_message_documents;
DROP TABLE IF EXISTS ai_document_jobs;
DROP TABLE IF EXISTS ai_document_chunks;
DROP TABLE IF EXISTS ai_documents;

-- +goose Up
-- Asynchronous Notebook research (POST /notebook/chats/:id/ask/jobs).
-- A job belongs to one user and one notebook; every read is scoped by both.
CREATE TABLE IF NOT EXISTS notebook_research_jobs (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id UUID NOT NULL REFERENCES notebook_chats(id) ON DELETE CASCADE,
    operation_id TEXT NOT NULL CHECK (operation_id <> '' AND length(operation_id) <= 128),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    stage TEXT NOT NULL DEFAULT 'queued',
    chunks_done INT NOT NULL DEFAULT 0 CHECK (chunks_done >= 0),
    chunks_total INT NOT NULL DEFAULT 0 CHECK (chunks_total >= 0),
    request JSONB NOT NULL,
    result JSONB,
    error JSONB,
    -- The executing instance extends the lease; an expired lease means the
    -- process stopped and the job is failed by the sweeper.
    lease_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    -- A repeated POST with the same operation_id returns the same job.
    UNIQUE (user_id, operation_id)
);

-- One unfinished job per user: the AI limiter allows two concurrent
-- generations per user and one deep job already uses both.
CREATE UNIQUE INDEX IF NOT EXISTS idx_notebook_research_jobs_active
    ON notebook_research_jobs(user_id)
    WHERE state IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS idx_notebook_research_jobs_chat
    ON notebook_research_jobs(chat_id, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notebook_research_jobs_lease
    ON notebook_research_jobs(lease_until)
    WHERE state IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS idx_notebook_research_jobs_finished
    ON notebook_research_jobs(finished_at)
    WHERE finished_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS notebook_research_jobs;

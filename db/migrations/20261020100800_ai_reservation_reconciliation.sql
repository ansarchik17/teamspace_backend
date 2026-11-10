-- +goose Up
CREATE TABLE ai_usage_reconciliation_jobs (
    attempt_id UUID PRIMARY KEY REFERENCES ai_usage_attempts(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','manual','completed')),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(next_attempt_at)),
    lease_token UUID,
    lease_until TIMESTAMPTZ CHECK (isfinite(lease_until)),
    claim_count BIGINT NOT NULL DEFAULT 0 CHECK (claim_count>=0),
    last_reason TEXT CHECK (length(last_reason) BETWEEN 1 AND 100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    completed_at TIMESTAMPTZ CHECK (isfinite(completed_at)),
    CHECK ((status='processing') = (lease_token IS NOT NULL)),
    CHECK ((status='processing') = (lease_until IS NOT NULL)),
    CHECK ((status='completed') = (completed_at IS NOT NULL))
);
CREATE INDEX ai_usage_reconciliation_jobs_due ON ai_usage_reconciliation_jobs(next_attempt_at,attempt_id) WHERE status='pending';
CREATE INDEX ai_usage_reconciliation_jobs_expired ON ai_usage_reconciliation_jobs(lease_until,attempt_id) WHERE status='processing';
CREATE INDEX ai_usage_reservations_active_age ON ai_usage_reservations(created_at,attempt_id) WHERE status='active';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM ai_usage_reconciliation_jobs) THEN
        RAISE EXCEPTION 'Cannot discard AI reconciliation history' USING ERRCODE='23514';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ai_usage_reconciliation_jobs;
DROP INDEX ai_usage_reservations_active_age;

-- +goose Up
CREATE TABLE ai_usage_failure_events (
    event_id UUID PRIMARY KEY,
    attempt_id UUID NOT NULL REFERENCES ai_usage_attempts(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('not_dispatched','confirmed_not_executed','confirmed_usage','unknown')),
    reason TEXT NOT NULL CHECK (reason IN ('local_error','provider_error','timeout','client_cancelled','invalid_usage')),
    evidence_source TEXT NOT NULL CHECK (length(evidence_source) BETWEEN 1 AND 100),
    evidence_reference TEXT CHECK (length(evidence_reference) BETWEEN 1 AND 200),
    result_fingerprint BYTEA NOT NULL CHECK (octet_length(result_fingerprint)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    CHECK (outcome <> 'confirmed_not_executed' OR evidence_reference IS NOT NULL)
);
CREATE INDEX ai_usage_failure_events_attempt ON ai_usage_failure_events(attempt_id);
-- +goose StatementBegin
CREATE FUNCTION ai_usage_failure_event_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'AI failure evidence is immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER ai_usage_failure_events_immutable BEFORE UPDATE OR DELETE ON ai_usage_failure_events
FOR EACH ROW EXECUTE FUNCTION ai_usage_failure_event_immutable();

-- Earlier executors did not persist a dispatch claim. Their existing reserved
-- attempts cannot prove non-dispatch. Keep their holds for reconciliation.
WITH legacy AS (
    UPDATE ai_usage_attempts SET status='unknown' WHERE status='reserved'
    RETURNING operation_id
)
UPDATE ai_usage_operations SET status='needs_reconciliation',completed_at=NULL
WHERE id IN (SELECT operation_id FROM legacy) AND status IN ('pending','running');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM ai_usage_failure_events) THEN
        RAISE EXCEPTION 'Cannot roll back nonempty AI failure evidence' USING ERRCODE='23514';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ai_usage_failure_events;
DROP FUNCTION ai_usage_failure_event_immutable();
-- Legacy unknown states remain conservative; never reset them to dispatchable.

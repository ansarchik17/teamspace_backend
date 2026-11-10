-- +goose Up
-- Final result evidence is separate from the immutable reservation price.
-- Actual model weights may differ from the requested model in the same policy.
CREATE TABLE ai_usage_settlements (
    attempt_id UUID PRIMARY KEY REFERENCES ai_usage_attempts(id) ON DELETE RESTRICT,
    result_fingerprint BYTEA NOT NULL CHECK (octet_length(result_fingerprint)=32),
    input_weight_microcredits BIGINT NOT NULL CHECK (input_weight_microcredits>0),
    output_weight_microcredits BIGINT CHECK (output_weight_microcredits>0),
    cached_input_weight_microcredits BIGINT CHECK (cached_input_weight_microcredits>0),
    reasoning_output_weight_microcredits BIGINT CHECK (reasoning_output_weight_microcredits>0),
    input_only BOOLEAN NOT NULL,
    overrun_microcredits BIGINT NOT NULL CHECK (overrun_microcredits>=0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(created_at))
);

-- +goose StatementBegin
CREATE FUNCTION ai_usage_settlement_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Final AI settlement evidence is immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER ai_usage_settlements_immutable BEFORE UPDATE OR DELETE ON ai_usage_settlements
FOR EACH ROW EXECUTE FUNCTION ai_usage_settlement_immutable();

-- +goose Down
-- Never silently discard the fingerprints/actual prices of settled attempts.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM ai_usage_settlements) THEN
        RAISE EXCEPTION 'Cannot roll back nonempty AI settlement evidence' USING ERRCODE='23514';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ai_usage_settlements;
DROP FUNCTION ai_usage_settlement_immutable();

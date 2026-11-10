-- +goose Up
ALTER TABLE ai_usage_attempts ADD COLUMN measured_unit TEXT CHECK (measured_unit IN ('audio_seconds','text_characters'));
ALTER TABLE ai_usage_attempts ADD COLUMN measured_quantity BIGINT CHECK (measured_quantity >= 0);

-- Replace only the original requirement for token counters on verified usage.
-- Existing constraints for negative/subset/total counters remain in force.
-- +goose StatementBegin
DO $$ DECLARE constraint_row RECORD; BEGIN
    FOR constraint_row IN SELECT conname FROM pg_constraint
        WHERE conrelid='ai_usage_attempts'::regclass AND contype='c'
          AND pg_get_constraintdef(oid) LIKE '%usage_source IS NOT NULL%'
          AND pg_get_constraintdef(oid) LIKE '%input_tokens IS NOT NULL%'
    LOOP
        EXECUTE format('ALTER TABLE ai_usage_attempts DROP CONSTRAINT %I', constraint_row.conname);
    END LOOP;
END $$;
-- +goose StatementEnd
ALTER TABLE ai_usage_attempts ADD CONSTRAINT ai_usage_verified_counters CHECK (
    usage_status NOT IN ('verified','derived') OR (usage_source IS NOT NULL AND (
        (measured_unit IS NULL AND measured_quantity IS NULL AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL)
        OR (credit_policy_version='audio-credits-v1' AND measured_unit IS NOT NULL AND measured_quantity IS NOT NULL
            AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND cached_input_tokens IS NULL AND reasoning_output_tokens IS NULL)
    ))
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ai_usage_journal_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mutable_fields TEXT[];
BEGIN
    IF TG_TABLE_NAME = 'ai_usage_operations' THEN
        mutable_fields := ARRAY['status','completed_at'];
    ELSIF TG_TABLE_NAME = 'ai_usage_attempts' THEN
        IF OLD.status IN ('settled','rejected','waived') AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'Final AI attempt is immutable' USING ERRCODE='23514';
        END IF;
        mutable_fields := ARRAY['actual_model','provider_request_id','reported_input_tokens','reported_output_tokens','reported_total_tokens','input_tokens','output_tokens','total_tokens','cached_input_tokens','reasoning_output_tokens','measured_unit','measured_quantity','usage_status','usage_source','charged_microcredits','status','completed_at'];
    ELSE
        IF OLD.status <> 'active' AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'Closed AI reservation is immutable' USING ERRCODE='23514';
        END IF;
        mutable_fields := ARRAY['status','closed_at','reconcile_after'];
    END IF;
    IF (to_jsonb(NEW) - mutable_fields) IS DISTINCT FROM (to_jsonb(OLD) - mutable_fields) THEN
        RAISE EXCEPTION 'AI journal identity and price snapshot are immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Audio evidence cannot be discarded or rewritten as text tokens.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Audio accounting migration requires a forward repair; evidence is retained'; END $$;
-- +goose StatementEnd

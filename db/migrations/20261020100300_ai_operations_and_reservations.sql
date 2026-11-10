-- +goose Up
-- No FK to chats/messages: deleting presentation history must not erase billing.
CREATE TABLE ai_usage_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organization_ai_budgets(organization_id) ON DELETE RESTRICT,
    user_id BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    payer_kind TEXT NOT NULL CHECK (payer_kind IN ('user', 'service')),
    operation_kind TEXT NOT NULL CHECK (length(operation_kind) BETWEEN 1 AND 100),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_fingerprint BYTEA NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    source_kind TEXT CHECK (length(source_kind) BETWEEN 1 AND 100),
    source_id UUID,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','cancelled','needs_reconciliation')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    completed_at TIMESTAMPTZ CHECK (isfinite(completed_at) AND completed_at >= created_at),
    CHECK (payer_kind <> 'user' OR user_id IS NOT NULL),
    CHECK ((source_kind IS NULL) = (source_id IS NULL)),
    CHECK ((status IN ('completed','failed','cancelled')) = (completed_at IS NOT NULL)),
    UNIQUE (id, organization_id)
);
-- A user's key cannot silently become a new operation after a company transfer.
CREATE UNIQUE INDEX ai_usage_operations_user_key ON ai_usage_operations(user_id,idempotency_key) WHERE payer_kind='user';
CREATE UNIQUE INDEX ai_usage_operations_service_key ON ai_usage_operations(organization_id,idempotency_key) WHERE payer_kind='service';
CREATE INDEX ai_usage_operations_company_created ON ai_usage_operations(organization_id,created_at,id);
CREATE INDEX ai_usage_operations_user_created ON ai_usage_operations(user_id,created_at,id) WHERE user_id IS NOT NULL;

-- One row per actual provider attempt, not per final chat answer.
CREATE TABLE ai_usage_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id UUID NOT NULL,
    organization_id UUID NOT NULL,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    provider_id TEXT NOT NULL CHECK (length(provider_id) BETWEEN 1 AND 100),
    endpoint_kind TEXT NOT NULL CHECK (length(endpoint_kind) BETWEEN 1 AND 100),
    requested_model TEXT NOT NULL CHECK (length(requested_model) BETWEEN 1 AND 200),
    actual_model TEXT CHECK (length(actual_model) BETWEEN 1 AND 200),
    provider_request_id TEXT CHECK (length(provider_request_id) BETWEEN 1 AND 300),
    credit_policy_version TEXT NOT NULL CHECK (length(credit_policy_version) BETWEEN 1 AND 100),
    usage_adapter_version TEXT NOT NULL CHECK (length(usage_adapter_version) BETWEEN 1 AND 100),
    input_weight_microcredits BIGINT NOT NULL CHECK (input_weight_microcredits > 0),
    output_weight_microcredits BIGINT CHECK (output_weight_microcredits > 0),
    cached_input_weight_microcredits BIGINT CHECK (cached_input_weight_microcredits > 0),
    reasoning_output_weight_microcredits BIGINT CHECK (reasoning_output_weight_microcredits > 0),
    company_period_id UUID NOT NULL,
    five_hour_period_id UUID REFERENCES user_ai_five_hour_periods(id) ON DELETE RESTRICT,
    reported_input_tokens BIGINT CHECK (reported_input_tokens >= 0),
    reported_output_tokens BIGINT CHECK (reported_output_tokens >= 0),
    reported_total_tokens BIGINT CHECK (reported_total_tokens >= 0),
    input_tokens BIGINT CHECK (input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens >= 0),
    total_tokens BIGINT CHECK (total_tokens >= 0),
    cached_input_tokens BIGINT CHECK (cached_input_tokens >= 0),
    reasoning_output_tokens BIGINT CHECK (reasoning_output_tokens >= 0),
    usage_status TEXT NOT NULL DEFAULT 'unknown' CHECK (usage_status IN ('unknown','invalid','verified','derived','confirmed_not_executed','platform_absorbed')),
    usage_source TEXT CHECK (length(usage_source) BETWEEN 1 AND 100),
    charged_microcredits BIGINT CHECK (charged_microcredits >= 0),
    status TEXT NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved','sent','unknown','settled','rejected','waived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    completed_at TIMESTAMPTZ CHECK (isfinite(completed_at) AND completed_at >= created_at),
    FOREIGN KEY (operation_id,organization_id) REFERENCES ai_usage_operations(id,organization_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id,company_period_id) REFERENCES organization_ai_weekly_periods(organization_id,id) ON DELETE RESTRICT,
    UNIQUE (operation_id,attempt_number),
    CHECK (cached_input_tokens IS NULL OR (input_tokens IS NOT NULL AND cached_input_tokens <= input_tokens)),
    CHECK (reasoning_output_tokens IS NULL OR (output_tokens IS NOT NULL AND reasoning_output_tokens <= output_tokens)),
    CHECK (usage_status NOT IN ('verified','derived') OR (input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND usage_source IS NOT NULL)),
    CHECK (usage_status NOT IN ('verified','derived') OR total_tokens IS NULL OR total_tokens::NUMERIC = input_tokens::NUMERIC + output_tokens::NUMERIC),
    CHECK (status <> 'settled' OR usage_status IN ('verified','derived')),
    CHECK (status <> 'rejected' OR usage_status = 'confirmed_not_executed'),
    CHECK (status <> 'waived' OR usage_status = 'platform_absorbed'),
    CHECK (status NOT IN ('rejected','waived') OR charged_microcredits = 0),
    CHECK ((status IN ('settled','rejected','waived')) = (charged_microcredits IS NOT NULL)),
    CHECK ((status IN ('settled','rejected','waived')) = (completed_at IS NOT NULL))
);
-- A known response belongs to one attempt; retries with new response IDs remain separate.
CREATE UNIQUE INDEX ai_usage_attempts_provider_request ON ai_usage_attempts(provider_id,provider_request_id) WHERE provider_request_id IS NOT NULL;
CREATE INDEX ai_usage_attempts_company_period ON ai_usage_attempts(organization_id,company_period_id);
CREATE INDEX ai_usage_attempts_five_hour ON ai_usage_attempts(five_hour_period_id) WHERE five_hour_period_id IS NOT NULL;
CREATE INDEX ai_usage_attempts_pending ON ai_usage_attempts(created_at,id) WHERE status IN ('reserved','sent','unknown');

CREATE TABLE ai_usage_reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL UNIQUE REFERENCES ai_usage_attempts(id) ON DELETE RESTRICT,
    reserved_microcredits BIGINT NOT NULL CHECK (reserved_microcredits > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','settled','released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    closed_at TIMESTAMPTZ CHECK (isfinite(closed_at) AND closed_at >= created_at),
    reconcile_after TIMESTAMPTZ CHECK (isfinite(reconcile_after) AND reconcile_after >= created_at),
    CHECK ((status <> 'active') = (closed_at IS NOT NULL))
);
CREATE INDEX ai_usage_reservations_reconcile ON ai_usage_reservations(reconcile_after,id) WHERE status='active';

-- Identity/price snapshots cannot be reassigned when a user moves companies.
-- Result fields are writable until finalization; later corrections need a
-- separate adjustment entry, not mutation of the original settled attempt.
-- +goose StatementBegin
CREATE FUNCTION ai_usage_journal_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mutable_fields TEXT[];
BEGIN
    IF TG_TABLE_NAME = 'ai_usage_operations' THEN
        mutable_fields := ARRAY['status','completed_at'];
    ELSIF TG_TABLE_NAME = 'ai_usage_attempts' THEN
        IF OLD.status IN ('settled','rejected','waived') AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'Final AI attempt is immutable' USING ERRCODE='23514';
        END IF;
        mutable_fields := ARRAY['actual_model','provider_request_id','reported_input_tokens','reported_output_tokens','reported_total_tokens','input_tokens','output_tokens','total_tokens','cached_input_tokens','reasoning_output_tokens','usage_status','usage_source','charged_microcredits','status','completed_at'];
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

CREATE TRIGGER ai_usage_operations_immutable BEFORE UPDATE ON ai_usage_operations FOR EACH ROW EXECUTE FUNCTION ai_usage_journal_immutable();
CREATE TRIGGER ai_usage_attempts_immutable BEFORE UPDATE ON ai_usage_attempts FOR EACH ROW EXECUTE FUNCTION ai_usage_journal_immutable();
CREATE TRIGGER ai_usage_reservations_immutable BEFORE UPDATE ON ai_usage_reservations FOR EACH ROW EXECUTE FUNCTION ai_usage_journal_immutable();

-- Validate the personal period against the historical operation's user, never
-- against users.organization_id (which may already have changed).
-- +goose StatementBegin
CREATE FUNCTION ai_usage_attempt_payer_period() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE payer TEXT; uid BIGINT; period_uid BIGINT;
BEGIN
    SELECT payer_kind,user_id INTO payer,uid FROM ai_usage_operations
    WHERE id=NEW.operation_id AND organization_id=NEW.organization_id;
    IF NOT FOUND THEN RETURN NEW; END IF; -- the composite FK reports the error
    IF payer='user' THEN
        SELECT user_id INTO period_uid FROM user_ai_five_hour_periods WHERE id=NEW.five_hour_period_id;
        IF NEW.five_hour_period_id IS NULL OR period_uid IS DISTINCT FROM uid THEN
            RAISE EXCEPTION 'AI attempt requires payer personal period' USING ERRCODE='23514';
        END IF;
    ELSIF NEW.five_hour_period_id IS NOT NULL THEN
        RAISE EXCEPTION 'Service AI attempt has no personal period' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER ai_usage_attempts_payer_period BEFORE INSERT OR UPDATE ON ai_usage_attempts FOR EACH ROW EXECUTE FUNCTION ai_usage_attempt_payer_period();

-- +goose Down
DROP TABLE ai_usage_reservations;
DROP TABLE ai_usage_attempts;
DROP FUNCTION ai_usage_attempt_payer_period();
DROP TABLE ai_usage_operations;
DROP FUNCTION ai_usage_journal_immutable();

-- +goose Up
-- Personal accounts without a company share the same immutable usage journal.
-- Existing organization history, weights, charges and overrides are preserved.
CREATE TABLE user_ai_personal_quotas (
 user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
 weekly_quota_microcredits BIGINT NOT NULL CHECK(weekly_quota_microcredits BETWEEN 0 AND 1000000000000000),
 five_hour_limit_microcredits BIGINT NOT NULL CHECK(five_hour_limit_microcredits BETWEEN 0 AND 1000000000000000),
 revision BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE user_ai_personal_quota_audit (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id),
 actor_id BIGINT NOT NULL REFERENCES users(id), created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 before_value JSONB, after_value JSONB NOT NULL
);
ALTER TABLE ai_usage_operations ALTER COLUMN organization_id DROP NOT NULL;
ALTER TABLE ai_usage_operations ADD CONSTRAINT ai_service_requires_company CHECK(payer_kind='user' OR organization_id IS NOT NULL);
ALTER TABLE ai_usage_attempts ALTER COLUMN organization_id DROP NOT NULL;
ALTER TABLE ai_usage_attempts ALTER COLUMN company_period_id DROP NOT NULL;
ALTER TABLE ai_usage_attempts ADD CONSTRAINT ai_company_period_presence CHECK((organization_id IS NULL)=(company_period_id IS NULL));
ALTER TABLE ai_usage_attempts ADD CONSTRAINT ai_attempt_operation_fk FOREIGN KEY(operation_id) REFERENCES ai_usage_operations(id) ON DELETE RESTRICT;
ALTER TABLE ai_user_weekly_periods ALTER COLUMN organization_id DROP NOT NULL;
CREATE UNIQUE INDEX ai_user_weekly_no_company ON ai_user_weekly_periods(user_id,starts_at) WHERE organization_id IS NULL;
-- Composite FKs do not check nullable keys. Validate the immutable owner as well.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ai_usage_attempt_payer_period() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE payer TEXT; uid BIGINT; period_uid BIGINT;
BEGIN
 SELECT payer_kind,user_id INTO payer,uid FROM ai_usage_operations
 WHERE id=NEW.operation_id AND organization_id IS NOT DISTINCT FROM NEW.organization_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'AI operation owner mismatch' USING ERRCODE='23514'; END IF;
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ai_user_weekly_attempt_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'Personal AI weekly assignment is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM ai_usage_attempts a JOIN ai_usage_operations o ON o.id=a.operation_id
 JOIN ai_user_weekly_periods p ON p.organization_id IS NOT DISTINCT FROM o.organization_id AND p.user_id=o.user_id
 WHERE a.id=NEW.attempt_id AND p.id=NEW.period_id AND o.payer_kind='user') THEN
 RAISE EXCEPTION 'Personal AI weekly period must belong to payer'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Personal AI policy rollback requires an explicit financial-history migration';
END $$;
-- +goose StatementEnd

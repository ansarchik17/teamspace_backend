-- +goose Up
-- Personal allowance periods are distinct from the company's accounting week.
CREATE TABLE ai_user_weekly_periods (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), organization_id UUID NOT NULL,user_id BIGINT NOT NULL,
 starts_at TIMESTAMPTZ NOT NULL CHECK(isfinite(starts_at)),ends_at TIMESTAMPTZ NOT NULL CHECK(isfinite(ends_at)),
 used_microcredits BIGINT NOT NULL DEFAULT 0 CHECK(used_microcredits>=0),
 FOREIGN KEY(organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
 FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE RESTRICT,
 UNIQUE(organization_id,user_id,starts_at), CHECK(ends_at-starts_at=interval '168 hours')
);
CREATE TABLE ai_user_weekly_attempts (
 attempt_id UUID PRIMARY KEY REFERENCES ai_usage_attempts(id) ON DELETE RESTRICT,
 period_id UUID NOT NULL REFERENCES ai_user_weekly_periods(id) ON DELETE RESTRICT
);
CREATE INDEX ai_user_weekly_attempts_period ON ai_user_weekly_attempts(period_id);
-- Backfill historical requests without changing immutable attempts or prices.
WITH attempts AS (
 SELECT a.id,o.organization_id,o.user_id,a.created_at,
 min(a.created_at) OVER(PARTITION BY o.organization_id,o.user_id) anchor
 FROM ai_usage_attempts a JOIN ai_usage_operations o ON o.id=a.operation_id WHERE o.payer_kind='user'
), windows AS (
 SELECT DISTINCT organization_id,user_id,anchor+floor(extract(epoch FROM(created_at-anchor))/604800)*interval '168 hours' starts_at FROM attempts
)
INSERT INTO ai_user_weekly_periods(organization_id,user_id,starts_at,ends_at)
SELECT organization_id,user_id,starts_at,starts_at+interval '168 hours' FROM windows;
INSERT INTO ai_user_weekly_attempts(attempt_id,period_id)
SELECT a.id,p.id FROM ai_usage_attempts a JOIN ai_usage_operations o ON o.id=a.operation_id
JOIN ai_user_weekly_periods p ON p.organization_id=o.organization_id AND p.user_id=o.user_id AND a.created_at>=p.starts_at AND a.created_at<p.ends_at
WHERE o.payer_kind='user';
UPDATE ai_user_weekly_periods p SET used_microcredits=COALESCE((SELECT sum(a.charged_microcredits)
 FROM ai_user_weekly_attempts l JOIN ai_usage_attempts a ON a.id=l.attempt_id WHERE l.period_id=p.id),0)::bigint;
CREATE TRIGGER ai_user_weekly_periods_immutable BEFORE UPDATE ON ai_user_weekly_periods FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();
-- +goose StatementBegin
CREATE FUNCTION ai_user_weekly_attempt_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'Personal AI weekly assignment is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM ai_usage_attempts a JOIN ai_usage_operations o ON o.id=a.operation_id
 JOIN ai_user_weekly_periods p ON p.organization_id=o.organization_id AND p.user_id=o.user_id
 WHERE a.id=NEW.attempt_id AND p.id=NEW.period_id AND o.payer_kind='user') THEN
 RAISE EXCEPTION 'Personal AI weekly period must belong to payer'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER ai_user_weekly_attempt_owner BEFORE INSERT OR UPDATE ON ai_user_weekly_attempts FOR EACH ROW EXECUTE FUNCTION ai_user_weekly_attempt_identity();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ai_user_weekly_attempts) THEN RAISE EXCEPTION 'Cannot discard personal weekly accounting history'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ai_user_weekly_attempts;
DROP FUNCTION ai_user_weekly_attempt_identity();
DROP TABLE ai_user_weekly_periods;

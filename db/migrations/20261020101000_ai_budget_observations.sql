-- +goose Up
-- Immutable admission snapshots. No prompts, provider payloads or credentials.
CREATE TABLE ai_usage_budget_observations (
 attempt_id UUID NOT NULL REFERENCES ai_usage_attempts(id) ON DELETE RESTRICT,
 scope TEXT NOT NULL CHECK (scope IN ('company','weekly','five_hour','service')),
 limit_microcredits BIGINT CHECK (limit_microcredits >= 0),
 used_microcredits BIGINT NOT NULL CHECK (used_microcredits >= 0),
 held_microcredits BIGINT NOT NULL CHECK (held_microcredits >= 0),
 requested_microcredits BIGINT NOT NULL CHECK (requested_microcredits > 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (attempt_id, scope)
);
CREATE INDEX ai_usage_budget_observations_created ON ai_usage_budget_observations(created_at);
-- Both estimated admission violations and actual overruns remain inspectable.
-- NULL actual charge means unknown, never a confirmed zero-cost call.
CREATE VIEW ai_usage_budget_exceedances AS
SELECT o.*, a.organization_id, a.charged_microcredits,
 CASE WHEN o.limit_microcredits IS NULL THEN 'unconfigured'
      WHEN o.limit_microcredits = 0 THEN 'disabled'
      ELSE 'exhausted' END AS reason,
 (o.limit_microcredits IS NULL OR o.used_microcredits::NUMERIC + o.held_microcredits + o.requested_microcredits > o.limit_microcredits) AS exceeded_at_admission,
 CASE WHEN a.charged_microcredits IS NULL OR o.limit_microcredits IS NULL THEN NULL
      ELSE o.used_microcredits::NUMERIC + o.held_microcredits + a.charged_microcredits > o.limit_microcredits END AS exceeded_with_actual_usage
FROM ai_usage_budget_observations o JOIN ai_usage_attempts a ON a.id=o.attempt_id
WHERE o.limit_microcredits IS NULL
   OR o.used_microcredits::NUMERIC + o.held_microcredits + o.requested_microcredits > o.limit_microcredits
   OR o.used_microcredits::NUMERIC + o.held_microcredits + a.charged_microcredits > o.limit_microcredits;

-- +goose Down
DROP VIEW ai_usage_budget_exceedances;
DROP TABLE ai_usage_budget_observations;

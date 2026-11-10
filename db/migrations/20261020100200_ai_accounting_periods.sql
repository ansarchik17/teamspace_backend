-- +goose Up
-- Anchors are created by the first accepted reservation, never by a GET.
CREATE TABLE organization_ai_weekly_anchors (
    organization_id UUID PRIMARY KEY REFERENCES organization_ai_budgets(organization_id) ON DELETE RESTRICT,
    anchor_at TIMESTAMPTZ NOT NULL CHECK (isfinite(anchor_at)),
    UNIQUE (organization_id, anchor_at)
);

CREATE TABLE user_ai_five_hour_anchors (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    anchor_at TIMESTAMPTZ NOT NULL CHECK (isfinite(anchor_at)),
    UNIQUE (user_id, anchor_at)
);

CREATE TABLE organization_ai_weekly_periods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL,
    anchor_at TIMESTAMPTZ NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL CHECK (isfinite(starts_at)),
    ends_at TIMESTAMPTZ NOT NULL CHECK (isfinite(ends_at)),
    used_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (used_microcredits >= 0),
    reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (reserved_microcredits >= 0),
    carried_reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (carried_reserved_microcredits >= 0 AND carried_reserved_microcredits <= reserved_microcredits),
    FOREIGN KEY (organization_id, anchor_at) REFERENCES organization_ai_weekly_anchors(organization_id, anchor_at) ON DELETE RESTRICT,
    UNIQUE (organization_id, starts_at),
    UNIQUE (organization_id, id),
    CHECK (CASE WHEN isfinite(starts_at) AND isfinite(ends_at) THEN ends_at - starts_at = INTERVAL '168 hours' ELSE false END),
    CHECK (CASE WHEN isfinite(starts_at) AND isfinite(anchor_at) THEN starts_at >= anchor_at AND mod(extract(epoch FROM (starts_at - anchor_at)), 604800) = 0 ELSE false END),
    CHECK (used_microcredits::NUMERIC + reserved_microcredits <= 9223372036854775807)
);

-- These rows share the exact company period through its FK, rather than
-- creating independent user weeks. Released assignments retain their history.
CREATE TABLE organization_ai_user_weekly_usage (
    organization_id UUID NOT NULL,
    period_id UUID NOT NULL,
    user_id BIGINT NOT NULL,
    used_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (used_microcredits >= 0),
    reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (reserved_microcredits >= 0),
    carried_reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (carried_reserved_microcredits >= 0 AND carried_reserved_microcredits <= reserved_microcredits),
    PRIMARY KEY (organization_id, period_id, user_id),
    FOREIGN KEY (organization_id, period_id) REFERENCES organization_ai_weekly_periods(organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_ai_user_quotas(organization_id, user_id) ON DELETE RESTRICT,
    CHECK (used_microcredits::NUMERIC + reserved_microcredits <= 9223372036854775807)
);
CREATE INDEX organization_ai_user_weekly_usage_user_idx ON organization_ai_user_weekly_usage(user_id, organization_id, period_id);

CREATE TABLE organization_ai_service_weekly_usage (
    organization_id UUID NOT NULL,
    period_id UUID NOT NULL,
    used_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (used_microcredits >= 0),
    reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (reserved_microcredits >= 0),
    carried_reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (carried_reserved_microcredits >= 0 AND carried_reserved_microcredits <= reserved_microcredits),
    PRIMARY KEY (organization_id, period_id),
    FOREIGN KEY (organization_id, period_id) REFERENCES organization_ai_weekly_periods(organization_id, id) ON DELETE RESTRICT,
    CHECK (used_microcredits::NUMERIC + reserved_microcredits <= 9223372036854775807)
);

-- Personal five-hour windows survive transfers and have no organization key.
CREATE TABLE user_ai_five_hour_periods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL,
    anchor_at TIMESTAMPTZ NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL CHECK (isfinite(starts_at)),
    ends_at TIMESTAMPTZ NOT NULL CHECK (isfinite(ends_at)),
    used_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (used_microcredits >= 0),
    reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (reserved_microcredits >= 0),
    carried_reserved_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (carried_reserved_microcredits >= 0 AND carried_reserved_microcredits <= reserved_microcredits),
    FOREIGN KEY (user_id, anchor_at) REFERENCES user_ai_five_hour_anchors(user_id, anchor_at) ON DELETE RESTRICT,
    UNIQUE (user_id, starts_at),
    CHECK (CASE WHEN isfinite(starts_at) AND isfinite(ends_at) THEN ends_at - starts_at = INTERVAL '5 hours' ELSE false END),
    CHECK (CASE WHEN isfinite(starts_at) AND isfinite(anchor_at) THEN starts_at >= anchor_at AND mod(extract(epoch FROM (starts_at - anchor_at)), 18000) = 0 ELSE false END),
    CHECK (used_microcredits::NUMERIC + reserved_microcredits <= 9223372036854775807)
);

-- A counter update cannot move history to another owner/window or reset an
-- anchor. No settings revisions are touched by accounting updates.
-- +goose StatementBegin
CREATE FUNCTION ai_accounting_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (to_jsonb(NEW) - ARRAY['used_microcredits','reserved_microcredits','carried_reserved_microcredits'])
       IS DISTINCT FROM
       (to_jsonb(OLD) - ARRAY['used_microcredits','reserved_microcredits','carried_reserved_microcredits']) THEN
        RAISE EXCEPTION 'AI accounting identity and boundaries are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER organization_ai_weekly_anchors_immutable BEFORE UPDATE ON organization_ai_weekly_anchors
FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();
CREATE TRIGGER user_ai_five_hour_anchors_immutable BEFORE UPDATE ON user_ai_five_hour_anchors
FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();
CREATE TRIGGER organization_ai_weekly_periods_immutable BEFORE UPDATE ON organization_ai_weekly_periods
FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();
CREATE TRIGGER organization_ai_user_weekly_usage_immutable BEFORE UPDATE ON organization_ai_user_weekly_usage
FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();
CREATE TRIGGER organization_ai_service_weekly_usage_immutable BEFORE UPDATE ON organization_ai_service_weekly_usage
FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();
CREATE TRIGGER user_ai_five_hour_periods_immutable BEFORE UPDATE ON user_ai_five_hour_periods
FOR EACH ROW EXECUTE FUNCTION ai_accounting_identity_immutable();

-- +goose Down
DROP TABLE user_ai_five_hour_periods;
DROP TABLE organization_ai_service_weekly_usage;
DROP TABLE organization_ai_user_weekly_usage;
DROP TABLE organization_ai_weekly_periods;
DROP TABLE user_ai_five_hour_anchors;
DROP TABLE organization_ai_weekly_anchors;
DROP FUNCTION ai_accounting_identity_immutable();

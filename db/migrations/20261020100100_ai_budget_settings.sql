-- +goose Up
-- Settings only. Periods, consumption, reservations and aggregate allocation
-- enforcement are introduced separately. No existing company receives AI credit.
CREATE TABLE organization_ai_budgets (
    organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE RESTRICT,
    weekly_budget_microcredits BIGINT NOT NULL CHECK (weekly_budget_microcredits BETWEEN 0 AND 1000000000000000),
    max_five_hour_microcredits BIGINT NOT NULL CHECK (max_five_hour_microcredits BETWEEN 0 AND 1000000000000000),
    service_weekly_quota_microcredits BIGINT NOT NULL DEFAULT 0 CHECK (service_weekly_quota_microcredits BETWEEN 0 AND 1000000000000000),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1)
);

CREATE TABLE organization_ai_user_quotas (
    organization_id UUID NOT NULL REFERENCES organization_ai_budgets(organization_id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    -- NULL/NULL is a released assignment, retained to prevent revision reuse.
    weekly_quota_microcredits BIGINT CHECK (weekly_quota_microcredits BETWEEN 0 AND 1000000000000000),
    five_hour_limit_microcredits BIGINT CHECK (five_hour_limit_microcredits BETWEEN 0 AND 1000000000000000),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    PRIMARY KEY (organization_id, user_id),
    CHECK ((weekly_quota_microcredits IS NULL) = (five_hour_limit_microcredits IS NULL))
);
CREATE INDEX organization_ai_user_quotas_user_idx ON organization_ai_user_quotas(user_id);

-- Revisions belong to settings, not consumption. Reject manual revision edits;
-- callers compare expected versions in their UPDATE predicate/transaction.
-- +goose StatementBegin
CREATE FUNCTION ai_budget_settings_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.revision IS DISTINCT FROM 1 THEN
            RAISE EXCEPTION 'Initial AI settings revision must be one' USING ERRCODE = '23514';
        END IF;
    ELSE
        IF NEW.organization_id IS DISTINCT FROM OLD.organization_id
           OR (TG_TABLE_NAME = 'organization_ai_user_quotas' AND to_jsonb(NEW)->'user_id' IS DISTINCT FROM to_jsonb(OLD)->'user_id') THEN
            RAISE EXCEPTION 'AI settings identity is immutable' USING ERRCODE = '23514';
        END IF;
        IF NEW.revision IS DISTINCT FROM OLD.revision THEN
            RAISE EXCEPTION 'AI settings revision is database managed' USING ERRCODE = '23514';
        END IF;
        IF (to_jsonb(NEW) - 'revision') IS DISTINCT FROM (to_jsonb(OLD) - 'revision') THEN
            NEW.revision := OLD.revision + 1;
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER organization_ai_budgets_revision BEFORE INSERT OR UPDATE ON organization_ai_budgets
FOR EACH ROW EXECUTE FUNCTION ai_budget_settings_revision();
CREATE TRIGGER organization_ai_user_quotas_revision BEFORE INSERT OR UPDATE ON organization_ai_user_quotas
FOR EACH ROW EXECUTE FUNCTION ai_budget_settings_revision();

-- Lock membership while assigning so concurrent transfers cannot attach a
-- usable quota to a foreign company. Released historical rows may remain.
-- +goose StatementBegin
CREATE FUNCTION ai_user_quota_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE member_org UUID; member_deleted TIMESTAMPTZ;
BEGIN
    IF NEW.weekly_quota_microcredits IS NOT NULL OR NEW.five_hour_limit_microcredits IS NOT NULL THEN
        SELECT organization_id, deleted_at INTO member_org, member_deleted
        FROM users WHERE id = NEW.user_id FOR SHARE;
        IF NOT FOUND OR member_org IS DISTINCT FROM NEW.organization_id OR member_deleted IS NOT NULL THEN
            RAISE EXCEPTION 'AI quota requires current company membership' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER organization_ai_user_quotas_membership BEFORE INSERT OR UPDATE ON organization_ai_user_quotas
FOR EACH ROW EXECUTE FUNCTION ai_user_quota_membership();

-- Release settings, not history, on transfer/soft deletion. A return to this
-- company must explicitly assign a new quota using the retained revision.
-- +goose StatementBegin
CREATE FUNCTION ai_release_user_quota() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL) THEN
        UPDATE organization_ai_user_quotas
        SET weekly_quota_microcredits = NULL, five_hour_limit_microcredits = NULL
        WHERE user_id = OLD.id AND organization_id = OLD.organization_id
          AND weekly_quota_microcredits IS NOT NULL;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER users_release_ai_quota AFTER UPDATE OF organization_id, deleted_at ON users
FOR EACH ROW EXECUTE FUNCTION ai_release_user_quota();

-- +goose Down
DROP TRIGGER users_release_ai_quota ON users;
DROP FUNCTION ai_release_user_quota();
DROP TABLE organization_ai_user_quotas;
DROP FUNCTION ai_user_quota_membership();
DROP TABLE organization_ai_budgets;
DROP FUNCTION ai_budget_settings_revision();

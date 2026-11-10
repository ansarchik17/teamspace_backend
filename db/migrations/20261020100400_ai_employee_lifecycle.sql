-- +goose Up
-- Membership changes and admission serialize on user -> company budget.
-- Lock both budgets in UUID order on transfer; never move accounting records.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ai_release_user_quota() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id IS NOT DISTINCT FROM OLD.organization_id
       AND NEW.deleted_at IS NOT DISTINCT FROM OLD.deleted_at
       AND NEW.is_active IS NOT DISTINCT FROM OLD.is_active THEN
        RETURN NEW;
    END IF;

    PERFORM organization_id FROM organization_ai_budgets
    WHERE organization_id IN (OLD.organization_id, NEW.organization_id)
    ORDER BY organization_id FOR UPDATE;

    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
        -- Clear assignments on departure AND return. Preserve rows/revisions,
        -- company weekly history, global five-hour history and active reserves.
        UPDATE organization_ai_user_quotas
        SET weekly_quota_microcredits = NULL, five_hour_limit_microcredits = NULL
        WHERE user_id = OLD.id
          AND organization_id IN (OLD.organization_id, NEW.organization_id)
          AND weekly_quota_microcredits IS NOT NULL;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

DROP TRIGGER users_release_ai_quota ON users;
CREATE TRIGGER users_release_ai_quota
AFTER UPDATE OF organization_id, deleted_at, is_active ON users
FOR EACH ROW EXECUTE FUNCTION ai_release_user_quota();

-- +goose Down
DROP TRIGGER users_release_ai_quota ON users;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ai_release_user_quota() RETURNS trigger LANGUAGE plpgsql AS $$
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

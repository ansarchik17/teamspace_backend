-- +goose Up
-- Product allowance consumed on an accepted terminal turn; never provider usage.
-- No FK to presentation history: deleting a chat must not refund consumed quota.
CREATE TABLE ai_final_request_cutoffs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id BIGINT NOT NULL REFERENCES users(id),
 organization_id UUID REFERENCES organizations(id),
 source_message_id UUID NOT NULL, conversation_id UUID NOT NULL, assistant_message_id UUID NOT NULL,
 weekly_period_id UUID NOT NULL REFERENCES ai_user_weekly_periods(id),
 five_hour_period_id UUID NOT NULL REFERENCES user_ai_five_hour_periods(id),
 microcredits BIGINT NOT NULL CHECK(microcredits>=0),
 scope TEXT NOT NULL CHECK(scope IN('weekly','five_hour')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(user_id,source_message_id)
);
-- +goose StatementBegin
CREATE FUNCTION ai_final_request_cutoff_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'Final AI quota cutoff is immutable' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM ai_user_weekly_periods w JOIN user_ai_five_hour_periods h ON h.id=NEW.five_hour_period_id
  WHERE w.id=NEW.weekly_period_id AND w.user_id=NEW.user_id AND h.user_id=NEW.user_id
  AND w.organization_id IS NOT DISTINCT FROM NEW.organization_id) THEN
  RAISE EXCEPTION 'Final AI quota cutoff requires payer personal periods' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER ai_final_request_cutoff_immutable BEFORE INSERT OR UPDATE OR DELETE ON ai_final_request_cutoffs
FOR EACH ROW EXECUTE FUNCTION ai_final_request_cutoff_immutable();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ai_final_request_cutoffs) THEN RAISE EXCEPTION 'Cannot discard consumed AI allowance'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ai_final_request_cutoffs;
DROP FUNCTION ai_final_request_cutoff_immutable();

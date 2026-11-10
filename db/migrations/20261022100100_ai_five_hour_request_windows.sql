-- +goose Up
-- Keep immutable historical anchors and period identities. Only the alignment
-- rule changes: a new personal period starts on an accepted request after idle.
-- +goose StatementBegin
DO $$
DECLARE alignment_constraint text;
BEGIN
    SELECT conname INTO STRICT alignment_constraint FROM pg_constraint
    WHERE conrelid='user_ai_five_hour_periods'::regclass AND contype='c'
      AND pg_get_constraintdef(oid) LIKE '%18000%';
    EXECUTE format('ALTER TABLE user_ai_five_hour_periods DROP CONSTRAINT %I', alignment_constraint);
END $$;
-- +goose StatementEnd
ALTER TABLE user_ai_five_hour_periods
    ADD CONSTRAINT user_ai_five_hour_starts_after_anchor CHECK (starts_at >= anchor_at);

-- Retain each unknown reservation and its weekly financial obligations.
-- The personal allowance contains only reservations belonging to that period.
UPDATE user_ai_five_hour_periods p
SET reserved_microcredits=COALESCE((SELECT SUM(r.reserved_microcredits)
    FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id
    WHERE a.five_hour_period_id=p.id AND r.status='active'),0)::bigint,
    carried_reserved_microcredits=0;

-- +goose Down
-- Refuse a rollback that would invalidate request-started historical windows.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM user_ai_five_hour_periods
        WHERE mod(extract(epoch FROM (starts_at-anchor_at)),18000) <> 0) THEN
        RAISE EXCEPTION 'Cannot restore fixed five-hour alignment with request-started history';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE user_ai_five_hour_periods DROP CONSTRAINT user_ai_five_hour_starts_after_anchor;
ALTER TABLE user_ai_five_hour_periods ADD CONSTRAINT user_ai_five_hour_fixed_alignment
CHECK (CASE WHEN isfinite(starts_at) AND isfinite(anchor_at)
    THEN starts_at >= anchor_at AND mod(extract(epoch FROM (starts_at-anchor_at)),18000)=0
    ELSE false END);
WITH holds AS (
    SELECT p.user_id,p.starts_at,r.reserved_microcredits amount
    FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id
    JOIN user_ai_five_hour_periods p ON p.id=a.five_hour_period_id WHERE r.status='active'
)
UPDATE user_ai_five_hour_periods p
SET reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds
    WHERE user_id=p.user_id AND starts_at<=p.starts_at),0)::bigint,
    carried_reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds
    WHERE user_id=p.user_id AND starts_at<p.starts_at),0)::bigint;

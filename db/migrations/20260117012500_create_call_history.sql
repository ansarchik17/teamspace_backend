-- +goose Up
CREATE TABLE IF NOT EXISTS call_history (
                                            id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
                                            call_id        uuid NOT NULL UNIQUE,
                                            from_user_id   bigint NOT NULL,
                                            to_user_id     bigint NOT NULL,
                                            room_id        text NOT NULL,
                                            chat_id        text,
                                            from_name      text,

                                            status         text NOT NULL DEFAULT 'initiated', -- initiated|accepted|missed|canceled|ended (на будущее)
                                            created_at     timestamptz NOT NULL DEFAULT now(),
                                            updated_at     timestamptz NOT NULL DEFAULT now(),
                                            ended_at       timestamptz
);

CREATE INDEX IF NOT EXISTS idx_call_history_to_user_created_at
    ON call_history (to_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_call_history_from_user_created_at
    ON call_history (from_user_id, created_at DESC);

-- чтобы удобнее было обновлять updated_at триггером — опционально
-- (если у тебя уже есть общий trigger set_updated_at(), используй его)
-- CREATE TRIGGER trg_call_history_updated_at
-- BEFORE UPDATE ON call_history
-- FOR EACH ROW EXECUTE PROCEDURE set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS call_history;
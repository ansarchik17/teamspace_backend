-- +goose Up
ALTER TABLE ai_messages ADD COLUMN data_cards JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE ai_messages ADD COLUMN data_sources JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE ai_messages DROP COLUMN data_sources;
ALTER TABLE ai_messages DROP COLUMN data_cards;

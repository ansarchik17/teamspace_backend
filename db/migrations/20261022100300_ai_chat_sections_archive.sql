-- +goose Up
CREATE TABLE ai_chat_sections (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 description TEXT NOT NULL DEFAULT '' CHECK (char_length(description)<=2000),
 color TEXT NOT NULL DEFAULT '#2458D3' CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,id)
);
ALTER TABLE ai_conversations ADD COLUMN section_id UUID, ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN archived BOOLEAN NOT NULL DEFAULT false, ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE ai_conversations ADD CONSTRAINT ai_chat_section_owner FOREIGN KEY(user_id,section_id)
 REFERENCES ai_chat_sections(user_id,id) ON DELETE SET NULL (section_id);
CREATE INDEX ai_chat_sections_owner ON ai_chat_sections(user_id,updated_at DESC);
CREATE INDEX ai_conversations_section ON ai_conversations(user_id,section_id);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ai_chat_sections) OR EXISTS(SELECT 1 FROM ai_conversations WHERE section_id IS NOT NULL OR pinned OR archived) THEN
 RAISE EXCEPTION 'Cannot discard AI chat organization data'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ai_conversations DROP CONSTRAINT ai_chat_section_owner;
ALTER TABLE ai_conversations DROP COLUMN section_id,DROP COLUMN pinned,DROP COLUMN archived,DROP COLUMN archived_at;
DROP TABLE ai_chat_sections;

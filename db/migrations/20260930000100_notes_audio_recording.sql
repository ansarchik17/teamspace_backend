-- +goose Up
-- NTS-004: Audio notes - add audio support to notes and audio attachments table

ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_type_check;
ALTER TABLE notes ADD CONSTRAINT notes_type_check CHECK (type IN ('text', 'checklist', 'rich', 'audio'));

CREATE TABLE IF NOT EXISTS note_audio_attachments (
    id           UUID         PRIMARY KEY,
    note_id      UUID         NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    storage_key  TEXT         NOT NULL,
    file_name    TEXT         NOT NULL DEFAULT '',
    mime_type    VARCHAR(100) NOT NULL,
    size_bytes   BIGINT       NOT NULL DEFAULT 0,
    duration     NUMERIC(10,2) NULL,
    waveform     JSONB        NOT NULL DEFAULT '[]'::jsonb,
    is_recording BOOLEAN      NOT NULL DEFAULT FALSE,
    status       TEXT         NOT NULL DEFAULT 'uploaded' CHECK (status IN ('pending', 'uploaded', 'deleted')),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_note_audio_attachments_note_status  ON note_audio_attachments(note_id, status);
CREATE INDEX IF NOT EXISTS idx_note_audio_attachments_user_created ON note_audio_attachments(user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS note_audio_attachments;

ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_type_check;
ALTER TABLE notes ADD CONSTRAINT notes_type_check CHECK (type IN ('text', 'checklist', 'rich'));

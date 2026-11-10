-- +goose Up
CREATE TABLE note_folders (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE notes (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id UUID REFERENCES note_folders(id) ON DELETE SET NULL,
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'text' CHECK (type IN ('text', 'checklist')),
    checklist_items JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMPTZ NULL,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    search_vector tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))
    ) STORED
);

CREATE TABLE note_attachments (
    note_id UUID NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (note_id, file_id)
);

CREATE TABLE note_audit_events (
    id UUID PRIMARY KEY,
    note_id UUID NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_note_folders_user_sort ON note_folders(user_id, sort_order, created_at);
CREATE INDEX idx_notes_user_deleted_pinned_updated ON notes(user_id, is_deleted, is_pinned, updated_at DESC, id DESC);
CREATE INDEX idx_notes_user_folder_deleted_updated ON notes(user_id, folder_id, is_deleted, updated_at DESC, id DESC);
CREATE INDEX idx_notes_user_archived_deleted_updated ON notes(user_id, is_archived, is_deleted, updated_at DESC, id DESC);
CREATE INDEX idx_notes_user_sync ON notes(user_id, updated_at ASC, id ASC);
CREATE INDEX idx_notes_search_vector ON notes USING GIN (search_vector);
CREATE INDEX idx_note_attachments_note_id ON note_attachments(note_id, created_at DESC);
CREATE INDEX idx_note_audit_events_note_created ON note_audit_events(note_id, created_at DESC);
CREATE INDEX idx_note_audit_events_user_created ON note_audit_events(user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS note_audit_events;
DROP TABLE IF EXISTS note_attachments;
DROP TABLE IF EXISTS notes;
DROP TABLE IF EXISTS note_folders;

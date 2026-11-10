-- +goose Up
ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_type_check;
ALTER TABLE notes ADD CONSTRAINT notes_type_check CHECK (type IN ('text', 'checklist', 'rich'));

-- +goose Down
ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_type_check;
ALTER TABLE notes ADD CONSTRAINT notes_type_check CHECK (type IN ('text', 'checklist'));

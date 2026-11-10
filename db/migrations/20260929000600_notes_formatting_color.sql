-- +goose Up
-- Follow the calendar release already applied in production.
ALTER TABLE notes ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT '';
ALTER TABLE notes ADD CONSTRAINT notes_color_valid CHECK (color = '' OR color ~ '^#[0-9a-fA-F]{6}$');

-- +goose Down
ALTER TABLE notes DROP COLUMN color;

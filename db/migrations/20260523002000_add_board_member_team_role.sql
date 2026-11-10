-- +goose Up
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS team_role VARCHAR(100);

-- +goose Down
ALTER TABLE board_members
    DROP COLUMN IF EXISTS team_role;

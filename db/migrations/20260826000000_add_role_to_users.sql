-- +goose Up
-- Add role column to users table (executed before admin seed)
ALTER TABLE users
ADD COLUMN role TEXT NOT NULL DEFAULT 'user';

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS role;

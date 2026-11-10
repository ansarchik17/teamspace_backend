-- +goose Up
ALTER TABLE users ADD COLUMN availability_status VARCHAR(50) DEFAULT 'offline';
UPDATE users SET availability_status = 'offline';

-- +goose Down
ALTER TABLE users DROP COLUMN availability_status;

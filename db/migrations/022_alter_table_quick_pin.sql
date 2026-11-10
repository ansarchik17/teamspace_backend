-- +goose Up
ALTER TABLE quick_pin_credential
    ALTER COLUMN expires_at DROP NOT NULL;

-- +goose Down
ALTER TABLE quick_pin_credential
    ALTER COLUMN expires_at SET NOT NULL;
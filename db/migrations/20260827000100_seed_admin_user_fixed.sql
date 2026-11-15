-- +goose Up
-- Retired security-sensitive bootstrap. Schema migrations must not provision
-- a privileged identity with a password shared by every installation.
-- Keep the version so existing Goose ledgers remain compatible. The forward
-- migration 20261115000100 retires unchanged accounts on existing databases.
SELECT 1;

-- +goose Down
-- Never delete a separately provisioned administrator or restore shared credentials.
SELECT 1;

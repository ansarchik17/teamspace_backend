-- +goose Up
-- Legacy payloads cannot be reconstructed from billing metadata. NULL means
-- unverified historical identity and never permits another provider dispatch.
ALTER TABLE ai_usage_attempts ADD COLUMN request_fingerprint BYTEA
    CHECK (request_fingerprint IS NULL OR octet_length(request_fingerprint)=32);
-- The existing journal immutability trigger automatically protects this field.

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM ai_usage_attempts WHERE request_fingerprint IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot discard AI attempt request identity' USING ERRCODE='23514';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ai_usage_attempts DROP COLUMN request_fingerprint;

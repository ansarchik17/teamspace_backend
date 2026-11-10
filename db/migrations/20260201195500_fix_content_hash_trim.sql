-- +goose Up
-- +goose StatementBegin
-- Fix content_hash to ignore leading/trailing whitespace
UPDATE messages 
SET content_hash = encode(digest(TRIM(COALESCE(content, '')), 'sha256'), 'hex')
WHERE content IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert is not strictly possible as we lost the original non-trimmed hashes, 
-- but we can re-hash without trim if needed. For now, we do nothing or re-calc without trim.
UPDATE messages 
SET content_hash = encode(digest(COALESCE(content, ''), 'sha256'), 'hex')
WHERE content IS NOT NULL;
-- +goose StatementEnd

-- +goose Up

-- alemgpt-intent is not available to the production provider team. Migrate
-- existing conversations so they work immediately after deployment.
UPDATE ai_conversations
SET model_name = 'openai/gpt-oss-120b', updated_at = NOW()
WHERE model_name = 'alemgpt-intent';

UPDATE unified_ai_chats
SET model_name = 'openai/gpt-oss-120b', updated_at = NOW()
WHERE model_name = 'alemgpt-intent';

-- +goose Down

-- Deliberately do not restore the restricted model. Reverting it would make
-- all migrated production conversations fail again.

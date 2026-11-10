-- +goose Up
-- +goose StatementBegin
ALTER TABLE channel_posts ADD COLUMN IF NOT EXISTS search_vector tsvector;

CREATE OR REPLACE FUNCTION messages_search_vector_update()
RETURNS trigger AS $$
BEGIN
    NEW.search_vector := setweight(to_tsvector('russian', COALESCE(NEW.content, '')), 'A') ||
                         setweight(to_tsvector('russian', COALESCE(NEW.attachment_name, '')), 'B');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS messages_search_vector_trigger ON messages;
CREATE TRIGGER messages_search_vector_trigger
    BEFORE INSERT OR UPDATE OF content, attachment_name ON messages
    FOR EACH ROW EXECUTE FUNCTION messages_search_vector_update();

UPDATE messages
SET search_vector = setweight(to_tsvector('russian', COALESCE(content, '')), 'A') ||
                    setweight(to_tsvector('russian', COALESCE(attachment_name, '')), 'B');

CREATE OR REPLACE FUNCTION channel_posts_search_vector_update()
RETURNS trigger AS $$
BEGIN
    NEW.search_vector := setweight(to_tsvector('russian', COALESCE(NEW.content, '')), 'A') ||
                         setweight(to_tsvector('russian', COALESCE(NEW.attachment_name, '')), 'B');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS channel_posts_search_vector_trigger ON channel_posts;
CREATE TRIGGER channel_posts_search_vector_trigger
    BEFORE INSERT OR UPDATE OF content, attachment_name ON channel_posts
    FOR EACH ROW EXECUTE FUNCTION channel_posts_search_vector_update();

UPDATE channel_posts
SET search_vector = setweight(to_tsvector('russian', COALESCE(content, '')), 'A') ||
                    setweight(to_tsvector('russian', COALESCE(attachment_name, '')), 'B');

CREATE INDEX IF NOT EXISTS idx_channel_posts_search_vector ON channel_posts USING GIN(search_vector);
CREATE INDEX IF NOT EXISTS idx_messages_content_trgm ON messages USING GIN (lower(content) gin_trgm_ops) WHERE content IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_attachment_name_trgm ON messages USING GIN (lower(attachment_name) gin_trgm_ops) WHERE attachment_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_posts_content_trgm ON channel_posts USING GIN (lower(content) gin_trgm_ops) WHERE content IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_posts_attachment_name_trgm ON channel_posts USING GIN (lower(attachment_name) gin_trgm_ops) WHERE attachment_name IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_channel_posts_search_vector;
DROP INDEX IF EXISTS idx_messages_content_trgm;
DROP INDEX IF EXISTS idx_messages_attachment_name_trgm;
DROP INDEX IF EXISTS idx_channel_posts_content_trgm;
DROP INDEX IF EXISTS idx_channel_posts_attachment_name_trgm;
DROP TRIGGER IF EXISTS channel_posts_search_vector_trigger ON channel_posts;
DROP FUNCTION IF EXISTS channel_posts_search_vector_update();
ALTER TABLE channel_posts DROP COLUMN IF EXISTS search_vector;

DROP TRIGGER IF EXISTS messages_search_vector_trigger ON messages;
CREATE OR REPLACE FUNCTION messages_search_vector_update()
RETURNS trigger AS $$
BEGIN
    NEW.search_vector := setweight(to_tsvector('russian', COALESCE(NEW.content, '')), 'A');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER messages_search_vector_trigger
    BEFORE INSERT OR UPDATE OF content ON messages
    FOR EACH ROW EXECUTE FUNCTION messages_search_vector_update();

UPDATE messages
SET search_vector = setweight(to_tsvector('russian', COALESCE(content, '')), 'A');
-- +goose StatementEnd

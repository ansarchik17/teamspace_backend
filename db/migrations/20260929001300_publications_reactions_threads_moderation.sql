-- +goose Up
-- +goose StatementBegin

-- 1. Publication Emoji Reactions
CREATE TABLE IF NOT EXISTS publication_reactions (
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reaction VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (publication_id, user_id, reaction)
);

CREATE INDEX IF NOT EXISTS idx_publication_reactions_pub ON publication_reactions(publication_id);
CREATE INDEX IF NOT EXISTS idx_publication_reactions_user ON publication_reactions(user_id);

-- 2. Publication Comment Threads, Pinning & Moderation
ALTER TABLE publication_comments
    ADD COLUMN IF NOT EXISTS parent_id UUID REFERENCES publication_comments(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS reply_to_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pinned_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS status VARCHAR(30) NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS moderation_reason TEXT,
    ADD COLUMN IF NOT EXISTS moderated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS moderated_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_publication_comments_parent ON publication_comments(publication_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_publication_comments_pinned ON publication_comments(publication_id, is_pinned DESC, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_publication_comments_status ON publication_comments(publication_id, status);

-- 3. Comment Emoji Reactions
CREATE TABLE IF NOT EXISTS publication_comment_reactions (
    comment_id UUID NOT NULL REFERENCES publication_comments(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reaction VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (comment_id, user_id, reaction)
);

CREATE INDEX IF NOT EXISTS idx_publication_comment_reactions_comment ON publication_comment_reactions(comment_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS publication_comment_reactions;

DROP INDEX IF EXISTS idx_publication_comments_status;
DROP INDEX IF EXISTS idx_publication_comments_pinned;
DROP INDEX IF EXISTS idx_publication_comments_parent;

ALTER TABLE publication_comments
    DROP COLUMN IF EXISTS moderated_at,
    DROP COLUMN IF EXISTS moderated_by,
    DROP COLUMN IF EXISTS moderation_reason,
    DROP COLUMN IF EXISTS is_hidden,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS pinned_by,
    DROP COLUMN IF EXISTS pinned_at,
    DROP COLUMN IF EXISTS is_pinned,
    DROP COLUMN IF EXISTS reply_to_user_id,
    DROP COLUMN IF EXISTS parent_id;

DROP TABLE IF EXISTS publication_reactions;

-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

-- ===== CHANNELS =====
CREATE TABLE channels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    username    VARCHAR(64) UNIQUE,              -- уникальный handle (@my_channel)
    avatar_url  TEXT,
    type        VARCHAR(16) NOT NULL DEFAULT 'public'  -- 'public' | 'private'
                CHECK (type IN ('public', 'private')),
    created_by  BIGINT NOT NULL REFERENCES users(id),
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    member_count INT NOT NULL DEFAULT 0,         -- денормализованный счётчик
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX idx_channels_username   ON channels(username) WHERE username IS NOT NULL;
CREATE INDEX idx_channels_created_by ON channels(created_by);
CREATE INDEX idx_channels_type       ON channels(type);

-- ===== CHANNEL MEMBERS (подписчики) =====
CREATE TABLE channel_members (
    channel_id  UUID   NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    role        VARCHAR(16) NOT NULL DEFAULT 'subscriber'
                CHECK (role IN ('owner', 'admin', 'subscriber')),
    is_muted    BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX idx_channel_members_user_id    ON channel_members(user_id);
CREATE INDEX idx_channel_members_channel_id ON channel_members(channel_id);

-- ===== CHANNEL POSTS =====
CREATE TABLE channel_posts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID   NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    author_id       BIGINT NOT NULL REFERENCES users(id),
    content         TEXT,
    attachment_url  TEXT,
    attachment_type VARCHAR(64),
    attachment_size BIGINT,
    is_pinned       BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted      BOOLEAN NOT NULL DEFAULT FALSE,
    is_edited       BOOLEAN NOT NULL DEFAULT FALSE,
    is_forwarded    BOOLEAN NOT NULL DEFAULT FALSE,
    original_channel_id UUID REFERENCES channels(id),
    original_post_id    UUID,
    views_count     INT NOT NULL DEFAULT 0,
    comments_count  INT NOT NULL DEFAULT 0,
    linked_group_id UUID REFERENCES groups(id),
    metadata        JSONB,
    edited_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_channel_posts_channel_id ON channel_posts(channel_id, created_at DESC);
CREATE INDEX idx_channel_posts_author_id  ON channel_posts(author_id);
CREATE INDEX idx_channel_posts_is_pinned  ON channel_posts(channel_id, is_pinned) WHERE is_pinned = TRUE;

-- ===== POST VIEWS =====
CREATE TABLE channel_post_views (
    post_id    UUID   NOT NULL REFERENCES channel_posts(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id),
    viewed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE INDEX idx_post_views_post_id ON channel_post_views(post_id);

-- ===== POST REACTIONS =====
CREATE TABLE channel_post_reactions (
    post_id    UUID   NOT NULL REFERENCES channel_posts(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id),
    reaction   VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id, reaction)
);

-- ===== TRIGGER: обновляем member_count =====
CREATE OR REPLACE FUNCTION update_channel_member_count()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' AND NEW.left_at IS NULL THEN
        UPDATE channels SET member_count = member_count + 1 WHERE id = NEW.channel_id;
    ELSIF TG_OP = 'UPDATE' AND OLD.left_at IS NULL AND NEW.left_at IS NOT NULL THEN
        UPDATE channels SET member_count = member_count - 1 WHERE id = NEW.channel_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_channel_member_count
AFTER INSERT OR UPDATE ON channel_members
FOR EACH ROW EXECUTE FUNCTION update_channel_member_count();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_channel_member_count ON channel_members;
DROP FUNCTION IF EXISTS update_channel_member_count();
DROP TABLE IF EXISTS channel_post_reactions;
DROP TABLE IF EXISTS channel_post_views;
DROP TABLE IF EXISTS channel_posts;
DROP TABLE IF EXISTS channel_members;
DROP TABLE IF EXISTS channels;
-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

CREATE TABLE publications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    
    -- Audit fields
    author_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    approved_by_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    
    -- Publication source type
    source_type VARCHAR(50) NOT NULL DEFAULT 'user',
    
    -- Publication type
    type VARCHAR(50) NOT NULL DEFAULT 'news'
        CHECK (type IN ('news', 'announcement', 'document', 'event', 'appeal')),
        
    title VARCHAR(500) NOT NULL,
    content TEXT,
    
    -- Visibility targeting
    target_audience VARCHAR(50) NOT NULL DEFAULT 'all',
    
    importance VARCHAR(50) NOT NULL DEFAULT 'normal'
        CHECK (importance IN ('normal', 'important', 'urgent')),
        
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    allow_comments BOOLEAN NOT NULL DEFAULT TRUE,
    requires_acknowledgement BOOLEAN NOT NULL DEFAULT FALSE,
    
    scheduled_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    views_count INT NOT NULL DEFAULT 0,
    shares_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_publications_author ON publications(author_id);
CREATE INDEX idx_publications_published_at ON publications(published_at DESC);

-- Attachments
CREATE TABLE publication_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    file_url TEXT NOT NULL,
    file_name VARCHAR(255) NOT NULL,
    file_type VARCHAR(100) NOT NULL, -- 'image' | 'file' | 'link'
    file_size BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_publication_attachments_pub ON publication_attachments(publication_id);

-- Likes
CREATE TABLE publication_likes (
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (publication_id, user_id)
);

CREATE INDEX idx_publication_likes_pub ON publication_likes(publication_id);

-- Comments
CREATE TABLE publication_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    author_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_publication_comments_pub ON publication_comments(publication_id, created_at ASC);

-- Read acknowledgements
CREATE TABLE publication_acknowledgements (
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (publication_id, user_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS publication_acknowledgements;
DROP TABLE IF EXISTS publication_comments;
DROP TABLE IF EXISTS publication_likes;
DROP TABLE IF EXISTS publication_attachments;
DROP TABLE IF EXISTS publications;
-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

-- 1. Extend publications table with pinning, banners, and notification timestamps
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS pinned_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS pinned_until TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS is_banner BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS banner_until TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS banner_action_text VARCHAR(100) NULL,
    ADD COLUMN IF NOT EXISTS banner_action_url VARCHAR(500) NULL,
    ADD COLUMN IF NOT EXISTS banner_color VARCHAR(50) NOT NULL DEFAULT 'urgent',
    ADD COLUMN IF NOT EXISTS notification_sent_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_publications_is_pinned_until ON publications(is_pinned, pinned_until) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_publications_is_banner_until ON publications(is_banner, banner_until) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_publications_importance ON publications(importance) WHERE deleted_at IS NULL;

-- 2. Banner dismissals per user
CREATE TABLE IF NOT EXISTS publication_banner_dismissals (
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (publication_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_banner_dismissals_user ON publication_banner_dismissals(user_id);

-- 3. Broadcast history and tracking
CREATE TABLE IF NOT EXISTS publication_broadcasts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    triggered_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    importance VARCHAR(50) NOT NULL,
    target_audience VARCHAR(50) NOT NULL,
    sent_push_count INT NOT NULL DEFAULT 0,
    sent_email_count INT NOT NULL DEFAULT 0,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status VARCHAR(50) NOT NULL DEFAULT 'completed',
    error_message TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_publication_broadcasts_pub_id ON publication_broadcasts(publication_id, sent_at DESC);

-- Successful deliveries are journalled so a retry does not resend them.
CREATE TABLE IF NOT EXISTS publication_broadcast_deliveries (
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel VARCHAR(10) NOT NULL CHECK (channel IN ('push', 'email')),
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (publication_id, user_id, channel)
);

CREATE TABLE IF NOT EXISTS publication_broadcast_queue (
    publication_id UUID PRIMARY KEY REFERENCES publications(id) ON DELETE CASCADE,
    triggered_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NULL
);
CREATE INDEX IF NOT EXISTS idx_publication_broadcast_queue_next ON publication_broadcast_queue(next_attempt_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS publication_broadcasts;
DROP TABLE IF EXISTS publication_broadcast_queue;
DROP TABLE IF EXISTS publication_broadcast_deliveries;
DROP TABLE IF EXISTS publication_banner_dismissals;

ALTER TABLE publications
    DROP COLUMN IF EXISTS notification_sent_at,
    DROP COLUMN IF EXISTS banner_color,
    DROP COLUMN IF EXISTS banner_action_url,
    DROP COLUMN IF EXISTS banner_action_text,
    DROP COLUMN IF EXISTS banner_until,
    DROP COLUMN IF EXISTS is_banner,
    DROP COLUMN IF EXISTS pinned_until,
    DROP COLUMN IF EXISTS pinned_by,
    DROP COLUMN IF EXISTS pinned_at;
-- +goose StatementEnd

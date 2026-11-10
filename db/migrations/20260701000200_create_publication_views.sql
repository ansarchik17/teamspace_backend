-- +goose Up
-- +goose StatementBegin
CREATE TABLE publication_views (
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (publication_id, user_id)
);

CREATE INDEX idx_publication_views_pub ON publication_views(publication_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS publication_views;
-- +goose StatementEnd
